variable "idp_certificate" {
  description = "The IDP certificate for SAML authentication"
  type        = string
  sensitive   = true
}

variable "idp_private_key" {
  description = "The IDP private key for SAML authentication"
  type        = string
  sensitive   = true
}
