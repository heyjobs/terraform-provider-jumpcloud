# Data Sources Examples

# jumpcloud_user Data Source
# Use this data source to get the ID of a JumpCloud user by their email address.
data "jumpcloud_user" "example" {
  email = "user@example.com"
}

output "user_id" {
  value = data.jumpcloud_user.example.id
}

output "user_username" {
  value = data.jumpcloud_user.example.username
}

# jumpcloud_user_group Data Source
# Use this data source to get the ID of a JumpCloud user group by name.
data "jumpcloud_user_group" "developers" {
  group_name = "developers"
}

output "group_id" {
  value = data.jumpcloud_user_group.developers.id
}

output "group_members" {
  value = data.jumpcloud_user_group.developers.members
}

# jumpcloud_application Data Source
# Use this data source to get the ID of a JumpCloud application.
data "jumpcloud_application" "aws" {
  display_label = "AWS"
}

output "app_id" {
  value = data.jumpcloud_application.aws.id
}
