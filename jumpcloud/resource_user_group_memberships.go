package jumpcloud

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const (
	// maxConcurrentGroupOps is the maximum number of concurrent group membership operations
	maxConcurrentGroupOps = 5
	// groupOpRateLimitMs is the minimum time between operations per worker (rate limiting)
	groupOpRateLimitMs = 20
	// maxRetries is the maximum number of retries for API calls
	maxRetries = 3
	// baseBackoffMs is the base backoff time in milliseconds for exponential backoff
	baseBackoffMs = 100
)

// groupOperation represents a single group membership operation
type groupOperation struct {
	groupID   string
	groupName string
	op        string // "add" or "remove"
}

func resourceUserGroupMemberships() *schema.Resource {
	return &schema.Resource{
		Description: "Manages all group memberships for a JumpCloud user as a single resource. " +
			"This resource looks up the user by email and groups by name, then manages " +
			"the memberships. Use this instead of multiple jumpcloud_user_group_membership " +
			"resources when you want to manage all of a user's group memberships in one place.",
		Create: resourceUserGroupMembershipsCreate,
		Read:   resourceUserGroupMembershipsRead,
		Update: resourceUserGroupMembershipsUpdate,
		Delete: resourceUserGroupMembershipsDelete,
		Schema: map[string]*schema.Schema{
			"user_email": {
				Description: "The email address of the JumpCloud user.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true, // Changing user requires new resource
			},
			"user_id": {
				Description: "The ID of the JumpCloud user (computed from email).",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"groups": {
				Description: "List of group names that the user should be a member of.",
				Type:        schema.TypeSet,
				Required:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"group_ids": {
				Description: "Map of group names to their IDs (computed).",
				Type:        schema.TypeMap,
				Computed:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
		},
		Importer: &schema.ResourceImporter{
			State: userGroupMembershipsImporter,
		},
	}
}

func userGroupMembershipsImporter(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	// Import by user email
	userEmail := d.Id()

	config := m.(*jcapiv2.Configuration)
	configv1 := convertV2toV1Config(config)
	clientv1 := jcapiv1.NewAPIClient(configv1)

	// Look up user by email
	user, err := getUserDetails(clientv1, userEmail)
	if err != nil {
		return nil, fmt.Errorf("error looking up user by email %s: %s", userEmail, err)
	}

	d.SetId(user.Id)
	_ = d.Set("user_email", userEmail)
	_ = d.Set("user_id", user.Id)

	// Read current memberships
	if err := resourceUserGroupMembershipsRead(d, m); err != nil {
		return nil, err
	}

	return []*schema.ResourceData{d}, nil
}

// groupLookupResult represents the result of a single group lookup
type groupLookupResult struct {
	name  string
	id    string
	err   error
}

// lookupGroupsByName looks up multiple groups by name concurrently and returns a map of name -> ID
func lookupGroupsByName(client *jcapiv2.APIClient, groupNames []string) (map[string]string, error) {
	result := make(map[string]string)

	if len(groupNames) == 0 {
		return result, nil
	}

	log.Printf("[DEBUG] lookupGroupsByName: Looking up %d groups concurrently", len(groupNames))

	// Determine number of workers
	numWorkers := maxConcurrentGroupOps
	if len(groupNames) < numWorkers {
		numWorkers = len(groupNames)
	}

	// Channels for work distribution and results
	nameChan := make(chan string, len(groupNames))
	resultChan := make(chan groupLookupResult, len(groupNames))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go groupLookupWorker(client, nameChan, resultChan, &wg)
	}

	// Send group names to workers
	for _, name := range groupNames {
		nameChan <- name
	}
	close(nameChan)

	// Wait for all workers to complete
	wg.Wait()
	close(resultChan)

	// Collect results
	var notFound []string
	var errors []string
	for res := range resultChan {
		if res.err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", res.name, res.err.Error()))
		} else if res.id == "" {
			notFound = append(notFound, res.name)
		} else {
			result[res.name] = res.id
		}
	}

	if len(errors) > 0 {
		return nil, fmt.Errorf("errors looking up groups:\n%s", strings.Join(errors, "\n"))
	}

	if len(notFound) > 0 {
		return nil, fmt.Errorf("groups not found: %s", strings.Join(notFound, ", "))
	}

	log.Printf("[DEBUG] lookupGroupsByName: Successfully looked up %d groups", len(result))
	return result, nil
}

// groupLookupWorker looks up groups by name from the channel with exponential backoff retry
func groupLookupWorker(client *jcapiv2.APIClient, names <-chan string, results chan<- groupLookupResult, wg *sync.WaitGroup) {
	defer wg.Done()

	for name := range names {
		filter := fmt.Sprintf(`{"name":"%s"}`, name)

		var lastErr error
		var foundID string

		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				backoff := time.Duration(baseBackoffMs*(1<<attempt)) * time.Millisecond
				log.Printf("[DEBUG] groupLookupWorker: Retry %d for group %s after %v", attempt, name, backoff)
				time.Sleep(backoff)
			}

			groups, _, err := client.UserGroupsApi.GroupsUserList(
				context.Background(),
				"application/json",
				"application/json",
				map[string]interface{}{
					"filter": filter,
					"limit":  int32(1),
				},
			)

			if err != nil {
				lastErr = err
				continue
			}

			// Find exact match (filter might return partial matches)
			lastErr = nil
			for _, group := range groups {
				if group.Name == name {
					foundID = group.Id
					break
				}
			}
			break
		}

		if lastErr != nil {
			results <- groupLookupResult{name: name, err: lastErr}
		} else {
			results <- groupLookupResult{name: name, id: foundID}
		}
		time.Sleep(groupOpRateLimitMs * time.Millisecond)
	}
}

// groupIDLookupResult represents the result of a single group ID lookup
type groupIDLookupResult struct {
	id   string
	name string
	err  error
}

// getGroupIDToNameMap looks up multiple groups by ID concurrently and returns a map of ID -> name
func getGroupIDToNameMap(client *jcapiv2.APIClient, groupIDs []string) (map[string]string, error) {
	result := make(map[string]string)

	if len(groupIDs) == 0 {
		return result, nil
	}

	log.Printf("[DEBUG] getGroupIDToNameMap: Looking up %d groups by ID concurrently", len(groupIDs))

	// Determine number of workers
	numWorkers := maxConcurrentGroupOps
	if len(groupIDs) < numWorkers {
		numWorkers = len(groupIDs)
	}

	// Channels for work distribution and results
	idChan := make(chan string, len(groupIDs))
	resultChan := make(chan groupIDLookupResult, len(groupIDs))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go groupIDLookupWorker(client, idChan, resultChan, &wg)
	}

	// Send group IDs to workers
	for _, id := range groupIDs {
		idChan <- id
	}
	close(idChan)

	// Wait for all workers to complete
	wg.Wait()
	close(resultChan)

	// Collect results
	var errors []string
	for res := range resultChan {
		if res.err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", res.id, res.err.Error()))
		} else if res.name != "" {
			result[res.id] = res.name
		}
		// If name is empty, group might have been deleted - skip silently
	}

	if len(errors) > 0 {
		return nil, fmt.Errorf("errors looking up groups by ID:\n%s", strings.Join(errors, "\n"))
	}

	log.Printf("[DEBUG] getGroupIDToNameMap: Successfully looked up %d groups", len(result))
	return result, nil
}

// groupIDLookupWorker looks up groups by ID from the channel with exponential backoff retry
func groupIDLookupWorker(client *jcapiv2.APIClient, ids <-chan string, results chan<- groupIDLookupResult, wg *sync.WaitGroup) {
	defer wg.Done()

	for id := range ids {
		var lastErr error
		var foundName string

		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				backoff := time.Duration(baseBackoffMs*(1<<attempt)) * time.Millisecond
				log.Printf("[DEBUG] groupIDLookupWorker: Retry %d for group ID %s after %v", attempt, id, backoff)
				time.Sleep(backoff)
			}

			group, _, err := client.UserGroupsApi.GroupsUserGet(
				context.Background(),
				id,
				"application/json",
				"application/json",
				nil,
			)

			if err != nil {
				// Check if group was deleted (404)
				if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
					lastErr = nil
					foundName = ""
					break
				}
				lastErr = err
				continue
			}

			lastErr = nil
			foundName = group.Name
			break
		}

		if lastErr != nil {
			results <- groupIDLookupResult{id: id, err: lastErr}
		} else {
			results <- groupIDLookupResult{id: id, name: foundName}
		}
		time.Sleep(groupOpRateLimitMs * time.Millisecond)
	}
}

func resourceUserGroupMembershipsCreate(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	configv1 := convertV2toV1Config(config)
	clientv1 := jcapiv1.NewAPIClient(configv1)
	clientv2 := jcapiv2.NewAPIClient(config)

	userEmail := d.Get("user_email").(string)

	// Look up user by email
	user, err := getUserDetails(clientv1, userEmail)
	if err != nil {
		return fmt.Errorf("error looking up user by email %s: %s", userEmail, err)
	}

	userID := user.Id
	d.SetId(userID)
	_ = d.Set("user_id", userID)

	// Get desired group names and look them up
	groupNamesSet := d.Get("groups").(*schema.Set)
	groupNames := make([]string, 0, groupNamesSet.Len())
	for _, name := range groupNamesSet.List() {
		groupNames = append(groupNames, name.(string))
	}

	groupNameToID, err := lookupGroupsByName(clientv2, groupNames)
	if err != nil {
		return err
	}

	// Store the group ID mapping
	_ = d.Set("group_ids", groupNameToID)

	// Get current group IDs (should be empty for new user, but check anyway)
	currentGroupIDs, err := getUserGroupIDs(clientv2, userID)
	if err != nil {
		return fmt.Errorf("error getting current group memberships: %s", err)
	}

	// Build list of desired group IDs
	desiredGroupIDs := make([]string, 0, len(groupNameToID))
	for _, id := range groupNameToID {
		desiredGroupIDs = append(desiredGroupIDs, id)
	}

	// Sync memberships
	if err := syncUserGroupsConcurrent(clientv2, userID, currentGroupIDs, desiredGroupIDs, groupNameToID); err != nil {
		return err
	}

	return resourceUserGroupMembershipsRead(d, m)
}

func resourceUserGroupMembershipsRead(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	clientv2 := jcapiv2.NewAPIClient(config)

	userID := d.Id()
	if userID == "" {
		return nil
	}

	// Get current group IDs for the user
	currentGroupIDs, err := getUserGroupIDs(clientv2, userID)
	if err != nil {
		// If user not found, remove from state
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			d.SetId("")
			return nil
		}
		return fmt.Errorf("error getting current group memberships: %s", err)
	}

	// Look up group names from IDs
	groupIDToName, err := getGroupIDToNameMap(clientv2, currentGroupIDs)
	if err != nil {
		return fmt.Errorf("error looking up group names: %s", err)
	}

	// Build the groups list and group_ids map
	groupNames := make([]string, 0, len(groupIDToName))
	groupIDs := make(map[string]string)
	for id, name := range groupIDToName {
		groupNames = append(groupNames, name)
		groupIDs[name] = id
	}

	// Sort for consistent ordering
	sort.Strings(groupNames)

	_ = d.Set("groups", groupNames)
	_ = d.Set("group_ids", groupIDs)

	return nil
}

func resourceUserGroupMembershipsUpdate(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	clientv2 := jcapiv2.NewAPIClient(config)

	userID := d.Id()

	if d.HasChange("groups") {
		// Get old and new group names
		oldGroupsRaw, newGroupsRaw := d.GetChange("groups")
		oldGroupsSet := oldGroupsRaw.(*schema.Set)
		newGroupsSet := newGroupsRaw.(*schema.Set)

		oldGroupNames := make([]string, 0, oldGroupsSet.Len())
		for _, name := range oldGroupsSet.List() {
			oldGroupNames = append(oldGroupNames, name.(string))
		}

		newGroupNames := make([]string, 0, newGroupsSet.Len())
		for _, name := range newGroupsSet.List() {
			newGroupNames = append(newGroupNames, name.(string))
		}

		// Look up all group names (old and new combined)
		allGroupNames := make(map[string]bool)
		for _, name := range oldGroupNames {
			allGroupNames[name] = true
		}
		for _, name := range newGroupNames {
			allGroupNames[name] = true
		}

		allGroupNamesList := make([]string, 0, len(allGroupNames))
		for name := range allGroupNames {
			allGroupNamesList = append(allGroupNamesList, name)
		}

		groupNameToID, err := lookupGroupsByName(clientv2, allGroupNamesList)
		if err != nil {
			return err
		}

		// Convert names to IDs
		oldGroupIDs := make([]string, 0, len(oldGroupNames))
		for _, name := range oldGroupNames {
			if id, ok := groupNameToID[name]; ok {
				oldGroupIDs = append(oldGroupIDs, id)
			}
		}

		newGroupIDs := make([]string, 0, len(newGroupNames))
		for _, name := range newGroupNames {
			if id, ok := groupNameToID[name]; ok {
				newGroupIDs = append(newGroupIDs, id)
			}
		}

		// Sync memberships concurrently
		if err := syncUserGroupsConcurrent(clientv2, userID, oldGroupIDs, newGroupIDs, groupNameToID); err != nil {
			return err
		}

		// Update group_ids map with only the new groups
		newGroupIDsMap := make(map[string]string)
		for _, name := range newGroupNames {
			if id, ok := groupNameToID[name]; ok {
				newGroupIDsMap[name] = id
			}
		}
		_ = d.Set("group_ids", newGroupIDsMap)
	}

	return resourceUserGroupMembershipsRead(d, m)
}

func resourceUserGroupMembershipsDelete(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	clientv2 := jcapiv2.NewAPIClient(config)

	userID := d.Id()

	// Get current group IDs
	currentGroupIDs, err := getUserGroupIDs(clientv2, userID)
	if err != nil {
		// If user not found, consider delete successful
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			return nil
		}
		return fmt.Errorf("error getting current group memberships: %s", err)
	}

	// Remove user from all groups (sync to empty list)
	if err := syncUserGroupsConcurrent(clientv2, userID, currentGroupIDs, []string{}, nil); err != nil {
		return err
	}

	d.SetId("")
	return nil
}

// syncUserGroupsConcurrent synchronizes a user's group memberships using concurrent API calls
func syncUserGroupsConcurrent(client *jcapiv2.APIClient, userID string, oldGroupIDs, newGroupIDs []string, groupNameToID map[string]string) error {
	// Build reverse lookup for logging
	groupIDToName := make(map[string]string)
	for name, id := range groupNameToID {
		groupIDToName[id] = name
	}

	// Convert slices to maps for efficient lookup
	oldGroups := make(map[string]bool)
	for _, id := range oldGroupIDs {
		if id != "" {
			oldGroups[id] = true
		}
	}

	newGroups := make(map[string]bool)
	for _, id := range newGroupIDs {
		if id != "" {
			newGroups[id] = true
		}
	}

	// Build list of operations
	var operations []groupOperation

	// Groups to add (in newGroups but not in oldGroups)
	for groupID := range newGroups {
		if !oldGroups[groupID] {
			operations = append(operations, groupOperation{
				groupID:   groupID,
				groupName: groupIDToName[groupID],
				op:        "add",
			})
		}
	}

	// Groups to remove (in oldGroups but not in newGroups)
	for groupID := range oldGroups {
		if !newGroups[groupID] {
			operations = append(operations, groupOperation{
				groupID:   groupID,
				groupName: groupIDToName[groupID],
				op:        "remove",
			})
		}
	}

	if len(operations) == 0 {
		log.Println("[DEBUG] syncUserGroupsConcurrent: No changes needed")
		return nil
	}

	log.Printf("[DEBUG] syncUserGroupsConcurrent: Processing %d group operations concurrently (max %d workers)", len(operations), maxConcurrentGroupOps)

	// Execute operations concurrently
	errors := executeGroupOperationsConcurrently(client, userID, operations)

	if len(errors) > 0 {
		return fmt.Errorf("group synchronization partially failed:\n%s", strings.Join(errors, "\n"))
	}

	return nil
}

// executeGroupOperationsConcurrently processes group membership operations using a worker pool
func executeGroupOperationsConcurrently(client *jcapiv2.APIClient, userID string, operations []groupOperation) []string {
	numWorkers := maxConcurrentGroupOps
	if len(operations) < numWorkers {
		numWorkers = len(operations)
	}

	// Channels for work distribution and results
	opsChan := make(chan groupOperation, len(operations))
	errChan := make(chan string, len(operations))

	// WaitGroup to track worker completion
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go groupOperationWorker(client, userID, opsChan, errChan, &wg)
	}

	// Send operations to workers
	for _, op := range operations {
		opsChan <- op
	}
	close(opsChan)

	// Wait for all workers to complete
	wg.Wait()
	close(errChan)

	// Collect errors
	var errors []string
	for errMsg := range errChan {
		errors = append(errors, errMsg)
	}

	addCount := 0
	removeCount := 0
	for _, op := range operations {
		if op.op == "add" {
			addCount++
		} else {
			removeCount++
		}
	}
	log.Printf("[DEBUG] syncUserGroupsConcurrent: Processed %d add operations, %d remove operations, %d errors",
		addCount, removeCount, len(errors))

	return errors
}

// groupOperationWorker processes group operations from the channel with exponential backoff retry
func groupOperationWorker(client *jcapiv2.APIClient, userID string, ops <-chan groupOperation, errors chan<- string, wg *sync.WaitGroup) {
	defer wg.Done()

	for op := range ops {
		opName := "Adding"
		if op.op == "remove" {
			opName = "Removing"
		}
		log.Printf("[DEBUG] %s user %s to/from group %s (%s)", opName, userID, op.groupName, op.groupID)

		payload := jcapiv2.UserGroupMembersReq{
			Op:    op.op,
			Type_: "user",
			Id:    userID,
		}
		req := map[string]interface{}{
			"body": payload,
		}

		var lastErr error
		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				backoff := time.Duration(baseBackoffMs*(1<<attempt)) * time.Millisecond
				log.Printf("[DEBUG] groupOperationWorker: Retry %d for %s user %s to/from group %s after %v",
					attempt, op.op, userID, op.groupName, backoff)
				time.Sleep(backoff)
			}

			res, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersPost(
				context.TODO(), op.groupID, "", "", req)

			if err != nil {
				lastErr = fmt.Errorf("error %s user %s to/from group %s (%s): %s; response = %+v",
					op.op, userID, op.groupName, op.groupID, err, res)
				continue
			}

			lastErr = nil
			break
		}

		if lastErr != nil {
			log.Printf("[ERROR] %s", lastErr.Error())
			errors <- lastErr.Error()
		}

		// Rate limiting between operations
		time.Sleep(groupOpRateLimitMs * time.Millisecond)
	}
}
