package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestUserResourceBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "email", rName+"@testorg.com"),
				),
			},
		},
	})
}

func testUserResourceConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
  			username = "%s"
			email = "%s@testorg.com"
		}`, name, name,
	)
}

func TestUserResourceWithGroups(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserResourceConfigWithGroups(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "email", rName+"@testorg.com"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "groups.#", "2"),
				),
			},
		},
	})
}

func testUserResourceConfigWithGroups(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user_group" "test_group_1" {
			name = "test_group_1_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_2" {
			name = "test_group_2_%[1]s"
		}

		resource "jumpcloud_user" "test_user" {
			username = "%[1]s"
			email    = "%[1]s@testorg.com"
			groups   = [
				jumpcloud_user_group.test_group_1.id,
				jumpcloud_user_group.test_group_2.id,
			]
		}
	`, name)
}

func TestUserResourceGroupsUpdate(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				// Start with 2 groups
				Config: testUserResourceConfigWithGroups(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "groups.#", "2"),
				),
			},
			{
				// Update to 3 groups
				Config: testUserResourceConfigWithGroupsUpdated(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "groups.#", "3"),
				),
			},
			{
				// Update to 1 group
				Config: testUserResourceConfigWithOneGroup(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "groups.#", "1"),
				),
			},
			{
				// Update to no groups
				Config: testUserResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "groups.#", "0"),
				),
			},
		},
	})
}

func testUserResourceConfigWithGroupsUpdated(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user_group" "test_group_1" {
			name = "test_group_1_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_2" {
			name = "test_group_2_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_3" {
			name = "test_group_3_%[1]s"
		}

		resource "jumpcloud_user" "test_user" {
			username = "%[1]s"
			email    = "%[1]s@testorg.com"
			groups   = [
				jumpcloud_user_group.test_group_1.id,
				jumpcloud_user_group.test_group_2.id,
				jumpcloud_user_group.test_group_3.id,
			]
		}
	`, name)
}

func testUserResourceConfigWithOneGroup(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user_group" "test_group_1" {
			name = "test_group_1_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_2" {
			name = "test_group_2_%[1]s"
		}

		resource "jumpcloud_user_group" "test_group_3" {
			name = "test_group_3_%[1]s"
		}

		resource "jumpcloud_user" "test_user" {
			username = "%[1]s"
			email    = "%[1]s@testorg.com"
			groups   = [
				jumpcloud_user_group.test_group_1.id,
			]
		}
	`, name)
}

func TestUserResourceImportWithGroups(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserResourceConfigWithGroups(rName),
			},
			{
				ResourceName:      "jumpcloud_user.test_user",
				ImportState:       true,
				ImportStateVerify: true,
				// Password is not returned by API, so we can't verify it on import
				ImportStateVerifyIgnore: []string{"password"},
			},
		},
	})
}
