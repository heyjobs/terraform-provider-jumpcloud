# Example: Managing all group memberships for a user in a single resource
#
# This resource manages ALL group memberships for a user as a single Terraform resource.
# It's more efficient than using multiple jumpcloud_user_group_membership resources.

# Simple example - single user with multiple groups
resource "jumpcloud_user_group_memberships" "developer" {
  user_email = "developer@example.com"

  groups = [
    "Datadog - Users",
    "PagerDuty - Users",
    "aws-web_staging-developer",
    "aws-web_production-developer",
  ]
}

# Example using for_each with a YAML file (recommended for managing many users)
#
# Given a jumpcloud_users.yaml file like:
# jumpcloud_users:
#   - name: user1@example.com
#     groups:
#       - Group A
#       - Group B
#   - name: user2@example.com
#     groups:
#       - Group C

resource "jumpcloud_user_group_memberships" "users" {
  for_each = {
    for user in yamldecode(file("${path.module}/jumpcloud_users.yaml"))["jumpcloud_users"] :
    user.name => user
  }

  user_email = each.value.name
  groups     = each.value.groups
}

# Output the computed user ID and group ID mappings
output "developer_user_id" {
  value = jumpcloud_user_group_memberships.developer.user_id
}

output "developer_group_ids" {
  value = jumpcloud_user_group_memberships.developer.group_ids
}
