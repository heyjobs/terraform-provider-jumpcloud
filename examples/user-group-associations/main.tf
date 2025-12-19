# Example: User Group Associations
# This resource associates user groups with various JumpCloud objects

# Create a user group
resource "jumpcloud_user_group" "engineering" {
  name = "engineering"
}

resource "jumpcloud_user_group" "devops" {
  name = "devops"
}

# Create a system group
resource "jumpcloud_system_group" "production_servers" {
  name = "Production Servers"
}

# Associate user group with a system group
resource "jumpcloud_user_group_association" "engineering_prod_systems" {
  group_id  = jumpcloud_user_group.engineering.id
  object_id = jumpcloud_system_group.production_servers.jc_id
  type      = "system_group"
}

# Associate user group with an application (using data source)
data "jumpcloud_application" "okta" {
  display_label = "Okta"
}

resource "jumpcloud_user_group_association" "devops_okta" {
  group_id  = jumpcloud_user_group.devops.id
  object_id = data.jumpcloud_application.okta.id
  type      = "application"
}

# Supported association types:
# - active_directory
# - application
# - command
# - g_suite
# - ldap_server
# - office_365
# - policy
# - radius_server
# - system
# - system_group
