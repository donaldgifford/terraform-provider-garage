terraform {
  required_providers {
    garage = {
      source = "donaldgifford/garage"
    }
  }
}

# Endpoint and admin bearer token can also be set via GARAGE_ENDPOINT and
# GARAGE_TOKEN environment variables.
provider "garage" {
  endpoint = "https://garage.example.com:3903"
  token    = var.garage_admin_token
}

variable "garage_admin_token" {
  type      = string
  sensitive = true
}
