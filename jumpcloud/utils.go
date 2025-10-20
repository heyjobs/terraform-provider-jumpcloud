package jumpcloud

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/go-resty/resty/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// Gets an application's metadata XML for SAML authentication
// this direct API call is a needed workaround since JumpCloud does not offer this endpoint through its SDK
func GetApplicationMetadataXml(orgId string, applicationId string, apiKey string) (string, error) {
	url := "https://console.jumpcloud.com/api/organizations/" + orgId + "/applications/" + applicationId + "/metadata.xml"

	// debug is always set to true, but output will only be shown if TF_LOG=DEBUG is set
	client := resty.New().SetDebug(true)

	resp, err := client.R().
		SetHeader("x-api-key", apiKey).
		Get(url)

	if err != nil {
		return "", err
	}

	log.Println("Status Code:", resp.StatusCode())
	log.Println("Status     :", resp.Status())
	log.Println("Time       :", resp.Time())
	log.Println("Received At:", resp.ReceivedAt())
	log.Println("Body       :\n", resp)

	return string(resp.Body()), nil
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func getUserGroupMemberIDs(client *jcapiv2.APIClient, groupID string) ([]string, error) {
	var userIds []string
	for i := 0; ; i++ {
		optionals := map[string]interface{}{
			"groupId": groupID,
			"limit":   int32(100),
			"skip":    int32(i * 100),
		}

		graphconnect, res, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersList(
			context.TODO(), groupID, "", "", optionals)
		if err != nil {
			return nil, fmt.Errorf("error getting group members for group id %s, error:%s; response = %+v", groupID, err, res)
		}

		for _, v := range graphconnect {
			userIds = append(userIds, v.To.Id)
		}

		if len(graphconnect) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return userIds, nil
}

func userIDsToEmails(configv2 *jcapiv2.Configuration, userIDs []string) ([]string, error) {
	emails := make([]string, len(userIDs))

	if len(userIDs) == 0 {
		return emails, nil
	}

	configv1 := convertV2toV1Config(configv2)
	client := jcapiv1.NewAPIClient(configv1)

	for i := 0; ; i++ {
		users, res, err := client.SystemusersApi.SystemusersList(context.TODO(), "", "", map[string]interface{}{
			"filter": "_id:$in:" + strings.Join(userIDs[:], "|"),
			"limit":  int32(100),
			"skip":   int32(i * 100),
			"fields": "email",
			"sort":   "email",
		})

		if err != nil {
			return nil, fmt.Errorf("error loading user emails from IDs: %s, i:%d, error:%s; response:%+v", userIDs, i, err, res)
		}

		for j, result := range users.Results {
			emails[j+(i*100)] = result.Email
		}

		if len(users.Results) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return emails, nil
}

func userEmailsToIDs(configv2 *jcapiv2.Configuration, userEmailsInterface []interface{}) ([]string, error) {
	userEmails := make([]string, len(userEmailsInterface))
	for i, userEmail := range userEmailsInterface {
		userEmails[i] = userEmail.(string)
	}

	ids := make([]string, len(userEmailsInterface))

	if len(userEmails) == 0 {
		return ids, nil
	}

	configv1 := convertV2toV1Config(configv2)
	client := jcapiv1.NewAPIClient(configv1)

	for i := 0; ; i++ {
		users, res, err := client.SystemusersApi.SystemusersList(context.TODO(), "", "", map[string]interface{}{
			"filter": "email:$in:" + strings.Join(userEmails[:], "|"),
			"limit":  int32(100),
			"skip":   int32(i * 100),
			"fields": "_id",
			"sort":   "_id",
		})

		if err != nil {
			return nil, fmt.Errorf("error loading user IDs from emails:%s; response = %+v", err, res)
		}

		for j, result := range users.Results {
			ids[j+(i*100)] = result.Id
		}

		if len(users.Results) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return ids, nil
}

func manageGroupMember(client *jcapiv2.APIClient, d *schema.ResourceData, memberID string, action string) error {
	payload := jcapiv2.UserGroupMembersReq{
		Op:    action,
		Type_: "user",
		Id:    memberID,
	}

	req := map[string]interface{}{
		"body": payload,
	}

	res, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersPost(
		context.TODO(), d.Id(), "", "", req)

	if err != nil {
		return fmt.Errorf("error managing group member, action: %s, member id:%s, error: %s; response = %+v", action, memberID, err, res)
	}
	return nil
}

// getUserGroupIDs returns all group IDs that a user belongs to
func getUserGroupIDs(client *jcapiv2.APIClient, userID string) ([]string, error) {
	if userID == "" {
		log.Println("[DEBUG] getUserGroupIDs: Empty user ID provided")
		return []string{}, nil
	}

	var groupIDs []string
	maxIterations := 100 // Prevent infinite loops (supports up to 10,000 groups)

	for i := 0; i < maxIterations; i++ {
		optionals := map[string]interface{}{
			"limit": int32(100),
			"skip":  int32(i * 100),
		}

		log.Printf("[DEBUG] getUserGroupIDs: Fetching groups for user %s (page %d)", userID, i+1)

		// Get all user group associations for this user
		associations, res, err := client.UsersApi.GraphUserAssociationsList(
			context.TODO(), userID, "user_group", "", []string{}, optionals)
		if err != nil {
			// Check if user doesn't exist or has been deleted
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
				log.Printf("[WARN] getUserGroupIDs: User %s not found, returning empty group list", userID)
				return []string{}, nil
			}
			return nil, fmt.Errorf("error getting user groups for user id %s, error:%s; response = %+v", userID, err, res)
		}

		for _, assoc := range associations {
			if assoc.To.Id != "" {
				groupIDs = append(groupIDs, assoc.To.Id)
			}
		}

		if len(associations) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}

	log.Printf("[DEBUG] getUserGroupIDs: Found %d groups for user %s", len(groupIDs), userID)
	return groupIDs, nil
}

// syncUserGroups synchronizes a user's group memberships
// It adds the user to new groups and removes from old groups
func syncUserGroups(client *jcapiv2.APIClient, userID string, oldGroupIDs, newGroupIDs []string) error {
	// Handle edge case: both lists are empty, nothing to do
	if len(oldGroupIDs) == 0 && len(newGroupIDs) == 0 {
		log.Println("[DEBUG] syncUserGroups: No groups to sync")
		return nil
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

	// Track errors but continue processing to sync as much as possible
	var addErrors []string
	var removeErrors []string
	addCount := 0
	removeCount := 0

	// Add user to new groups
	for groupID := range newGroups {
		if !oldGroups[groupID] {
			log.Printf("[DEBUG] syncUserGroups: Adding user %s to group %s", userID, groupID)
			payload := jcapiv2.UserGroupMembersReq{
				Op:    "add",
				Type_: "user",
				Id:    userID,
			}
			req := map[string]interface{}{
				"body": payload,
			}
			res, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersPost(
				context.TODO(), groupID, "", "", req)
			if err != nil {
				errMsg := fmt.Sprintf("error adding user %s to group %s: %s; response = %+v", userID, groupID, err, res)
				log.Printf("[ERROR] %s", errMsg)
				addErrors = append(addErrors, errMsg)
			} else {
				addCount++
				// Add small delay to avoid rate limiting
				if len(newGroups) > 10 {
					time.Sleep(50 * time.Millisecond)
				}
			}
		}
	}

	// Remove user from old groups
	for groupID := range oldGroups {
		if !newGroups[groupID] {
			log.Printf("[DEBUG] syncUserGroups: Removing user %s from group %s", userID, groupID)
			payload := jcapiv2.UserGroupMembersReq{
				Op:    "remove",
				Type_: "user",
				Id:    userID,
			}
			req := map[string]interface{}{
				"body": payload,
			}
			res, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersPost(
				context.TODO(), groupID, "", "", req)
			if err != nil {
				errMsg := fmt.Sprintf("error removing user %s from group %s: %s; response = %+v", userID, groupID, err, res)
				log.Printf("[ERROR] %s", errMsg)
				removeErrors = append(removeErrors, errMsg)
			} else {
				removeCount++
				// Add small delay to avoid rate limiting
				if len(oldGroups) > 10 {
					time.Sleep(50 * time.Millisecond)
				}
			}
		}
	}

	log.Printf("[DEBUG] syncUserGroups: Successfully added user to %d groups, removed from %d groups", addCount, removeCount)

	// Return combined error if any operations failed
	if len(addErrors) > 0 || len(removeErrors) > 0 {
		allErrors := append(addErrors, removeErrors...)
		return fmt.Errorf("group synchronization partially failed:\n%s", strings.Join(allErrors, "\n"))
	}

	return nil
}

// https://github.com/rootlyhq/terraform-provider-rootly/blob/99175a7ab4e154793ea8a8710d329a3f48eb0c90/tools/ignore_array_order.go#L12
func EqualIgnoringOrder(key, oldValue, newValue string, d *schema.ResourceData) bool {
	// The key is a path not the list itself, e.g. "events.0"
	lastDotIndex := strings.LastIndex(key, ".")
	if lastDotIndex != -1 {
		key = string(key[:lastDotIndex])
	}
	oldData, newData := d.GetChange(key)
	if oldData == nil || newData == nil {
		return false
	}
	oldArray := oldData.([]interface{})
	newArray := newData.([]interface{})
	if len(oldArray) != len(newArray) {
		// Items added or removed, always detect as changed
		return false
	}

	// Convert data to string arrays
	oldItems := make([]string, len(oldArray))
	newItems := make([]string, len(newArray))
	for i, oldItem := range oldArray {
		oldItems[i] = oldItem.(string)
	}
	for j, newItem := range newArray {
		newItems[j] = newItem.(string)
	}
	// Ensure consistent sorting before comparison, to suppress unnecessary change detections
	sort.Strings(oldItems)
	sort.Strings(newItems)
	return reflect.DeepEqual(oldItems, newItems)
}
