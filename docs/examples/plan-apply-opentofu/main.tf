# The built-in terraform_data resource needs no provider download.

variable "env" {
  type        = string
  description = "The environment the change is planned for."
}

resource "terraform_data" "release" {
  input = var.env
}
