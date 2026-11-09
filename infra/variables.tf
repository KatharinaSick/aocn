# Defaulted rather than required: this configuration manages one project, and the
# backend block cannot reference variables anyway, so the project is already fixed.

variable "project_id" {
  description = "GCP project ID."
  type        = string
  default     = "adventofcloudnative"
}

variable "region" {
  description = "Region for Artifact Registry and Cloud Run."
  type        = string
  default     = "europe-west1"
}

# The ID rather than owner/name: renaming the repository keeps its ID, so deploys survive
# a rename.
variable "github_repository_id" {
  description = "ID of the repository allowed to deploy (KatharinaSick/aocn)."
  type        = string
  default     = "1385333130"
}

# By ID, like the repository above. Its main branch uploads doors and can do nothing else.
variable "challenges_repository_id" {
  description = "ID of the private repository that uploads doors (KatharinaSick/aocn-challenges)."
  type        = string
  default     = "1426674968"
}
