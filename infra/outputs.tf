# None of these are confidential: impersonation is gated on the OIDC ref claim, not on
# knowing an identity's name. The workflows still read them from GitHub Actions variables
# rather than hardcoding them, which keeps environment-specific identifiers — including
# the GCP project number — out of a workflow file in a public repository.

output "wif_provider" {
  description = "Resource name of the Workload Identity Federation provider GitHub Actions authenticates against."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "deployer_service_account" {
  description = "Email of the service account GitHub Actions impersonates to deploy the app."
  value       = google_service_account.deployer.email
}

output "infra_plan_service_account" {
  description = "Email of the read-only service account pull requests plan with."
  value       = google_service_account.infra_plan.email
}

output "infra_apply_service_account" {
  description = "Email of the service account that applies infrastructure from main."
  value       = google_service_account.infra_apply.email
}

output "api_runtime_service_account" {
  description = "Email of the service account the Cloud Run service runs as."
  value       = google_service_account.api_runtime.email
}

output "docker_repository" {
  description = "Artifact Registry path that images are pushed to."
  value       = "${google_artifact_registry_repository.docker.location}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.docker.repository_id}"
}

output "state_bucket" {
  description = "Bucket holding this configuration's state."
  value       = google_storage_bucket.state.name
}

output "doors_bucket" {
  description = "Bucket holding every door's bundle and page."
  value       = google_storage_bucket.doors.name
}

output "challenges_wif_provider" {
  description = "Workload Identity Federation provider the challenges repository's workflows authenticate against."
  value       = google_iam_workload_identity_pool_provider.github_challenges.name
}

output "door_uploader_service_account" {
  description = "Email of the service account the challenges repository uploads doors as."
  value       = google_service_account.door_uploader.email
}

output "api_url" {
  description = "HTTPS URL the Cloud Run service is served at."
  value       = google_cloud_run_v2_service.api.uri
}
