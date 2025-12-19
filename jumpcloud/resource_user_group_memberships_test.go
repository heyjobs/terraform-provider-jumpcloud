package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

func TestUserGroupMembershipsResourceBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserGroupMembershipsResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user_group_memberships.test", "user_email", rName+"@testorg.com"),
					resource.TestCheckResourceAttrSet("jumpcloud_user_group_memberships.test", "user_id"),
					resource.TestCheckResourceAttr("jumpcloud_user_group_memberships.test", "groups.#", "2"),
				),
			},
		},
	})
}

func TestUserGroupMembershipsResourceUpdate(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				// Initial creation with 2 groups
				Config: testUserGroupMembershipsResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user_group_memberships.test", "groups.#", "2"),
				),
			},
			{
				// Update to 3 groups (add one, keep two)
				Config: testUserGroupMembershipsResourceConfigUpdated(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user_group_memberships.test", "groups.#", "3"),
				),
			},
			{
				// Update to 1 group (remove two)
				Config: testUserGroupMembershipsResourceConfigMinimal(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user_group_memberships.test", "groups.#", "1"),
				),
			},
		},
	})
}

func TestUserGroupMembershipsResourceImport(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserGroupMembershipsResourceConfigBasic(rName),
			},
			{
				ResourceName:      "jumpcloud_user_group_memberships.test",
				ImportState:       true,
				ImportStateId:     rName + "@testorg.com",
				ImportStateVerify: true,
			},
		},
	})
}

func testUserGroupMembershipsResourceConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
			username = "%[1]s"
			email    = "%[1]s@testorg.com"
		}

		resource "jumpcloud_user_group" "test_group_1" {
			name = "test_group_1_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_2" {
			name = "test_group_2_%[1]s"
		}

		resource "jumpcloud_user_group_memberships" "test" {
			user_email = jumpcloud_user.test_user.email
			groups = [
				jumpcloud_user_group.test_group_1.name,
				jumpcloud_user_group.test_group_2.name,
			]
		}
	`, name)
}

func testUserGroupMembershipsResourceConfigUpdated(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
			username = "%[1]s"
			email    = "%[1]s@testorg.com"
		}

		resource "jumpcloud_user_group" "test_group_1" {
			name = "test_group_1_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_2" {
			name = "test_group_2_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_3" {
			name = "test_group_3_%[1]s"
		}

		resource "jumpcloud_user_group_memberships" "test" {
			user_email = jumpcloud_user.test_user.email
			groups = [
				jumpcloud_user_group.test_group_1.name,
				jumpcloud_user_group.test_group_2.name,
				jumpcloud_user_group.test_group_3.name,
			]
		}
	`, name)
}

func testUserGroupMembershipsResourceConfigMinimal(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
			username = "%[1]s"
			email    = "%[1]s@testorg.com"
		}

		resource "jumpcloud_user_group" "test_group_1" {
			name = "test_group_1_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_2" {
			name = "test_group_2_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_3" {
			name = "test_group_3_%[1]s"
		}

		resource "jumpcloud_user_group_memberships" "test" {
			user_email = jumpcloud_user.test_user.email
			groups = [
				jumpcloud_user_group.test_group_1.name,
			]
		}
	`, name)
}
