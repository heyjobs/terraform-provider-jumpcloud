# Example: JumpCloud Application (AWS SSO)
resource "jumpcloud_application" "aws_sso" {
  name          = "aws"
  display_label = "AWS Production"
  sso_url       = "https://sso.jumpcloud.com/saml2/aws-production"
  sp_entity_id  = "urn:amazon:webservices"
  acs_url       = "https://signin.aws.amazon.com/saml"

  idp_entity_id   = "jumpcloud"
  idp_certificate = var.idp_certificate
  idp_private_key = var.idp_private_key

  constant_attributes {
    name  = "https://aws.amazon.com/SAML/Attributes/Role"
    value = "arn:aws:iam::123456789012:role/JumpCloudRole,arn:aws:iam::123456789012:saml-provider/JumpCloud"
  }

  constant_attributes {
    name  = "https://aws.amazon.com/SAML/Attributes/SessionDuration"
    value = "3600"
  }
}

# Associate a user group with the application
resource "jumpcloud_user_group_association" "aws_developers" {
  group_id  = jumpcloud_user_group.developers.id
  object_id = jumpcloud_application.aws_sso.id
  type      = "application"
}

resource "jumpcloud_user_group" "developers" {
  name = "developers"
}

# Data source: Look up an existing application
data "jumpcloud_application" "existing_app" {
  display_label = "AWS Production"
}

output "app_id" {
  value = data.jumpcloud_application.existing_app.id
}
