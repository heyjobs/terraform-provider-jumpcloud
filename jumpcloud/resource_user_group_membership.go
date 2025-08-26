package jumpcloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

func resourceUserGroupMembership() *schema.Resource {
	return &schema.Resource{
		Description: "Provides a resource for managing user group memberships.",
		Create:      resourceUserGroupMembershipCreate,
		Read:        resourceUserGroupMembershipRead,
		Update:      nil, // No update routine, as association cannot be updated
		Delete:      resourceUserGroupMembershipDelete,
		Schema: map[string]*schema.Schema{
			"userid": {
				Description: "The ID of the `resource_user` object.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"groupid": {
				Description: "The ID of the `resource_user_group` object.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
		},
		Importer: &schema.ResourceImporter{
			State: userGroupMembershipImporter,
		},
	}
}

func userGroupMembershipImporter(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	ids := strings.Split(d.Id(), "/")
	if len(ids) != 2 {
		return nil, fmt.Errorf("Invalid import format. Expected 'groupid/userid'")
	}
	groupID, userID := ids[0], ids[1]

	_ = d.Set("groupid", groupID)
	_ = d.Set("userid", userID)

	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	isMember, err := checkUserGroupMembership(client, groupID, userID)
	if err != nil {
		return nil, err
	}

	if isMember {
		d.SetId(groupID + "/" + userID)
		return []*schema.ResourceData{d}, nil
	}

	return nil, fmt.Errorf("User %s is not a member of group %s", userID, groupID)
}

func checkUserGroupMembership(client *jcapiv2.APIClient, groupID, userID string) (bool, error) {
	for i := 0; ; i++ {
		optionals := map[string]interface{}{
			"groupId": groupID,
			"limit":   int32(100),
			"skip":    int32(i * 100),
		}

		graphconnect, _, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersList(
			context.TODO(), groupID, "", "", optionals)
		if err != nil {
			return false, err
		}

		for _, v := range graphconnect {
			if v.To.Id == userID {
				return true, nil
			}
		}

		if len(graphconnect) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return false, nil
}

func modifyUserGroupMembership(client *jcapiv2.APIClient,
	d *schema.ResourceData, action string) error {

	payload := jcapiv2.UserGroupMembersReq{
		Op:    action,
		Type_: "user",
		Id:    d.Get("userid").(string),
	}

	req := map[string]interface{}{
		"body": payload,
	}

	_, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersPost(
		context.TODO(), d.Get("groupid").(string), "", "", req)

	return err
}

func resourceUserGroupMembershipCreate(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	err := modifyUserGroupMembership(client, d, "add")
	if err != nil {
		return err
	}
	return resourceUserGroupMembershipRead(d, m)
}

func resourceUserGroupMembershipRead(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	for i := 0; i < 20; i++ { // Prevent infinite loop

		optionals := map[string]interface{}{
			"groupId": d.Get("groupid").(string),
			"limit":   int32(100),
			"skip":    int32(i * 100),
		}

		graphconnect, _, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersList(
			context.TODO(), d.Get("groupid").(string), "", "", optionals)
		if err != nil {
			return err
		}

		for _, v := range graphconnect {
			if v.To.Id == d.Get("userid") {
				d.SetId(d.Get("groupid").(string) + "/" + d.Get("userid").(string))
				return nil
			}
		}

		if len(graphconnect) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Unset the ID to remove the resource from the state
	d.SetId("")
	return nil
}

func resourceUserGroupMembershipDelete(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)
	return modifyUserGroupMembership(client, d, "remove")
}
