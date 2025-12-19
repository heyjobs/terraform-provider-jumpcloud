# User Group with POSIX attributes
resource "jumpcloud_user_group" "test_group" {
  name = "test_group"
  attributes = {
    posix_groups = "32:testerino"
  }
}

# Basic user with MFA enabled
resource "jumpcloud_user" "test_user1" {
  username   = "testuser1"
  firstname  = "test"
  lastname   = "user1"
  email      = "testuser1@testorg.org"
  enable_mfa = true
}

# User without MFA
resource "jumpcloud_user" "test_user2" {
  username   = "testuser2"
  firstname  = "test"
  lastname   = "user2"
  email      = "testuser2@testorg.org"
  enable_mfa = false
}

# User with all available options
resource "jumpcloud_user" "admin_user" {
  username              = "adminuser"
  firstname             = "Admin"
  lastname              = "User"
  email                 = "admin@testorg.org"
  display_name          = "Admin User"
  enable_mfa            = true
  sudo                  = true
  passwordless_sudo     = false
  ldap_binding_user     = false
  password_never_expires = false
  suspended             = false

  # Assign directly to groups (preferred over jumpcloud_user_group_membership)
  groups = [
    jumpcloud_user_group.test_group.id
  ]

  phone_number {
    number = "+1-555-123-4567"
    type   = "mobile"
  }

  phone_number {
    number = "+1-555-987-6543"
    type   = "work"
  }
}

# User group membership (deprecated - prefer using 'groups' field on jumpcloud_user)
resource "jumpcloud_user_group_membership" "test_membership" {
  userid  = jumpcloud_user.test_user1.id
  groupid = jumpcloud_user_group.test_group.id
}
