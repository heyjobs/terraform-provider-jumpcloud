terraform {
  required_providers {
    jumpcloud = {
      source = "cheelim1/jumpcloud"
    }
  }
}

provider "jumpcloud" {
  api_key = var.jumpcloud_api_key
  org_id  = var.jumpcloud_org_id
}

variable "jumpcloud_api_key" {
  description = "JumpCloud API key"
  type        = string
  sensitive   = true
}

variable "jumpcloud_org_id" {
  description = "JumpCloud Organization ID (optional)"
  type        = string
  default     = ""
}
