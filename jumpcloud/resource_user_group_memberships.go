package jumpcloud

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// isRetryableStatus reports whether an API failure is worth retrying: a network-level failure
// (res is nil) or a rate-limit/server error. Any other 4xx (bad request, auth, permissions) is
// permanent - retrying it just burns maxRetries*backoff time before surfacing the same error.
func isRetryableStatus(res *http.Response) bool {
	return res == nil || res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
}

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
			"ignore_groups": {
				Description: "List of group names this resource must never manage: never added, " +
					"never removed, and never reported as drift. Use this for groups managed " +
					"outside Terraform (e.g. IT-managed groups) that this resource should not touch.",
				Type:     schema.TypeSet,
				Optional: true,
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

// lookupGroupsByName looks up multiple groups by name concurrently and returns a map of name -> ID.
// If failOnNotFound is false, names that don't resolve to a group are silently omitted from the
// result instead of causing an error (used for ignore_groups, where a nonexistent group is a no-op).
func lookupGroupsByName(client *jcapiv2.APIClient, groupNames []string, failOnNotFound bool) (map[string]string, error) {
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
		if failOnNotFound {
			return nil, fmt.Errorf("groups not found: %s", strings.Join(notFound, ", "))
		}
		log.Printf("[DEBUG] lookupGroupsByName: groups not found (ignored): %s", strings.Join(notFound, ", "))
	}

	log.Printf("[DEBUG] lookupGroupsByName: Successfully looked up %d groups", len(result))
	return result, nil
}

// groupNameOverlap returns the names present in both slices, sorted.
func groupNameOverlap(a, b []string) []string {
	set := make(map[string]bool, len(a))
	for _, name := range a {
		set[name] = true
	}
	var overlap []string
	for _, name := range b {
		if set[name] {
			overlap = append(overlap, name)
		}
	}
	sort.Strings(overlap)
	return overlap
}

// stringSetToSlice converts a *schema.Set of strings to a []string.
func stringSetToSlice(s *schema.Set) []string {
	list := make([]string, 0, s.Len())
	for _, v := range s.List() {
		list = append(list, v.(string))
	}
	return list
}

// intersectGroupIDs returns the ids from a that are also present in b.
func intersectGroupIDs(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, id := range b {
		set[id] = true
	}
	out := make([]string, 0, len(a))
	for _, id := range a {
		if set[id] {
			out = append(out, id)
		}
	}
	return out
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

			groups, res, err := client.UserGroupsApi.GroupsUserList(
				context.Background(),
				"application/json",
				"application/json",
				map[string]interface{}{
					"filter": filter,
					"limit":  int32(0),
					"sort":   []string{},
				},
			)

			if err != nil {
				lastErr = err
				if !isRetryableStatus(res) {
					break
				}
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
	groupNames := stringSetToSlice(d.Get("groups").(*schema.Set))
	ignoreGroupNames := stringSetToSlice(d.Get("ignore_groups").(*schema.Set))

	if overlap := groupNameOverlap(groupNames, ignoreGroupNames); len(overlap) > 0 {
		return fmt.Errorf("group(s) %s cannot be in both 'groups' and 'ignore_groups'", strings.Join(overlap, ", "))
	}

	groupNameToID, err := lookupGroupsByName(clientv2, groupNames, true)
	if err != nil {
		return err
	}

	// Store the group ID mapping
	_ = d.Set("group_ids", groupNameToID)

	// Get the user's real current membership, but only ever to check which declared groups
	// they're already in - never to discover undeclared groups as removal candidates.
	currentGroupIDs, err := getUserGroupIDs(clientv2, userID)
	if err != nil {
		return fmt.Errorf("error getting current group memberships: %s", err)
	}

	// Build list of desired group IDs
	desiredGroupIDs := make([]string, 0, len(groupNameToID))
	for _, id := range groupNameToID {
		desiredGroupIDs = append(desiredGroupIDs, id)
	}

	// The "old" set passed to the sync diff is current membership intersected with desired:
	// always a subset of desired, so the diff can only ever add, structurally never remove.
	alreadyMemberIDs := intersectGroupIDs(currentGroupIDs, desiredGroupIDs)

	// Sync memberships
	if err := syncUserGroupsConcurrent(clientv2, userID, alreadyMemberIDs, desiredGroupIDs, groupNameToID); err != nil {
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

	// Only ever check status for the groups this resource's own prior state declared - a real
	// group the user belongs to that was never declared must never appear here, in either
	// direction. Names that no longer resolve to a real group are treated as "not a member"
	// (surfaces as drift on the next plan) rather than failing Read outright.
	declaredGroupNames := stringSetToSlice(d.Get("groups").(*schema.Set))
	declaredNameToID, err := lookupGroupsByName(clientv2, declaredGroupNames, false)
	if err != nil {
		return fmt.Errorf("error looking up declared groups: %s", err)
	}

	// Get the user's real current membership, but only to check which declared groups they're
	// still actually in - never to discover or report on undeclared groups.
	currentGroupIDs, err := getUserGroupIDs(clientv2, userID)
	if err != nil {
		// If the user was deleted, remove this resource from state instead of leaving it
		// stuck showing 0 members forever.
		if errors.Is(err, errUserNotFound) {
			d.SetId("")
			return nil
		}
		return fmt.Errorf("error getting current group memberships: %s", err)
	}
	currentGroupSet := make(map[string]bool, len(currentGroupIDs))
	for _, id := range currentGroupIDs {
		currentGroupSet[id] = true
	}

	groupNames := make([]string, 0, len(declaredNameToID))
	groupIDs := make(map[string]string, len(declaredNameToID))
	for name, id := range declaredNameToID {
		if !currentGroupSet[id] {
			continue
		}
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

	if overlap := groupNameOverlap(stringSetToSlice(d.Get("groups").(*schema.Set)), stringSetToSlice(d.Get("ignore_groups").(*schema.Set))); len(overlap) > 0 {
		return fmt.Errorf("group(s) %s cannot be in both 'groups' and 'ignore_groups'", strings.Join(overlap, ", "))
	}

	if d.HasChange("groups") {
		// Get old and new group names
		oldGroupsRaw, newGroupsRaw := d.GetChange("groups")
		oldGroupNames := stringSetToSlice(oldGroupsRaw.(*schema.Set))
		newGroupNames := stringSetToSlice(newGroupsRaw.(*schema.Set))

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

		groupNameToID, err := lookupGroupsByName(clientv2, allGroupNamesList, true)
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

	// Remove only the groups this resource's own state declares. Never call getUserGroupIDs to
	// discover the user's full real membership for deletion purposes - that discovery is exactly
	// what let an earlier version of this resource strip undeclared, IT-managed groups on destroy.
	declaredGroupNames := stringSetToSlice(d.Get("groups").(*schema.Set))
	groupNameToID, err := lookupGroupsByName(clientv2, declaredGroupNames, false)
	if err != nil {
		return err
	}

	removeGroupIDs := make([]string, 0, len(groupNameToID))
	for _, id := range groupNameToID {
		removeGroupIDs = append(removeGroupIDs, id)
	}

	// A 404 removing an already-gone membership (group or user deleted) is handled as a no-op
	// success inside groupOperationWorker itself - idempotent destroy - so any error surfacing
	// here is a genuine failure, not just "already gone" dressed up in string-matched wording.
	if err := syncUserGroupsConcurrent(clientv2, userID, removeGroupIDs, []string{}, groupNameToID); err != nil {
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

	// Fail closed: refuse to touch any group this call can't name from its own declared set.
	// This should be unreachable given the allowlist-only callers above - it's a
	// belt-and-suspenders guard against ever repeating the incident where undeclared real
	// memberships got silently modified. Zero operations execute if this trips.
	var undeclared []string
	for _, op := range operations {
		if op.groupName == "" {
			undeclared = append(undeclared, op.groupID)
		}
	}
	if len(undeclared) > 0 {
		return fmt.Errorf("refusing to modify group(s) not present in the resource's own declared groups/ignore_groups: %v", undeclared)
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
				// A 404 removing a membership means it's already gone (group or user deleted) -
				// that's the desired end state, not a failure.
				if op.op == "remove" && res != nil && res.StatusCode == http.StatusNotFound {
					lastErr = nil
					break
				}
				lastErr = fmt.Errorf("error %s user %s to/from group %s (%s): %s; response = %+v",
					op.op, userID, op.groupName, op.groupID, err, res)
				if !isRetryableStatus(res) {
					break
				}
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
