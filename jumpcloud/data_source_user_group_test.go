package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestAccDataSourceJumpCloudUserGroup_basic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceJumpCloudUserGroupConfig(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.jumpcloud_user_group.test_group", "id"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test_group", "group_name", rName),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test_group", "members.#", "2"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test_group", "members.0", fmt.Sprintf("%s1@testorg.com", rName)),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test_group", "members.1", fmt.Sprintf("%s3@testorg.com", rName)),
				),
			},
		},
	})
}

func testAccDataSourceJumpCloudUserGroupConfig(groupName string) string {
	return fmt.Sprintf(`
resource "jumpcloud_user" "test_user1" {
  username = "%[1]s1"
  email = "%[1]s1@testorg.com"
  firstname = "Firstname"
  lastname = "Lastname"
  enable_mfa = true
}
resource "jumpcloud_user" "test_user2" {
  username = "%[1]s2"
  email = "%[1]s2@testorg.com"
  firstname = "Firstname"
  lastname = "Lastname"
  enable_mfa = true
}
resource "jumpcloud_user" "test_user3" {
  username = "%[1]s3"
  email = "%[1]s3@testorg.com"
  firstname = "Firstname"
  lastname = "Lastname"
  enable_mfa = true
}

resource "jumpcloud_user_group" "test_group" {
  name = "%[1]s"

  members = [
    jumpcloud_user.test_user1.email,
    jumpcloud_user.test_user3.email,
  ]
}

data "jumpcloud_user_group" "test_group" {
  group_name = jumpcloud_user_group.test_group.name
}`, groupName)
}
