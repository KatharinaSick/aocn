locals {
  services = [
    "artifactregistry.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "storage.googleapis.com",
    "sts.googleapis.com",
  ]

  secrets = [
    # The Neon connection string, password included.
    "database-url",
    "dynatrace-otlp-token",
    "resend-api-key",
    # Signs the link in the subscribe confirmation email, so an address is only added
    # by someone who can read its inbox.
    "subscribe-signing-key",
  ]
}

# Destroying this configuration should not disable APIs other resources still need.
resource "google_project_service" "enabled" {
  for_each = toset(local.services)

  service            = each.value
  disable_on_destroy = false
}

# ---------------------------------------------------------------------------
# State
# ---------------------------------------------------------------------------

resource "google_storage_bucket" "state" {
  name     = "${var.project_id}-tofu-state"
  location = var.region

  public_access_prevention    = "enforced"
  uniform_bucket_level_access = true

  versioning {
    enabled = true
  }

  lifecycle_rule {
    condition {
      num_newer_versions = 20
    }
    action {
      type = "Delete"
    }
  }

  # This bucket holds the state describing it.
  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.enabled]
}

# ---------------------------------------------------------------------------
# Doors
# ---------------------------------------------------------------------------

# Every door's bundle and page, uploaded weeks before it opens by the doors workflow in the
# private challenges repository. Nothing here is public: the API decides on each request
# whether a door is open and only then hands its files out.
resource "google_storage_bucket" "doors" {
  name     = "${var.project_id}-doors"
  location = var.region

  public_access_prevention    = "enforced"
  uniform_bucket_level_access = true

  # A broken upload is rolled back to the version before it.
  versioning {
    enabled = true
  }

  lifecycle_rule {
    condition {
      num_newer_versions = 10
    }
    action {
      type = "Delete"
    }
  }

  depends_on = [google_project_service.enabled]
}

# The API only reads. Doors are written by the challenges repository's upload workflow.
resource "google_storage_bucket_iam_member" "api_runtime_reads_doors" {
  bucket = google_storage_bucket.doors.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.api_runtime.email}"
}

# objectUser rather than objectCreator: an upload replaces the door's files, and replacing
# an object needs delete. The old version stays as a noncurrent version either way.
resource "google_storage_bucket_iam_member" "door_uploader_writes_doors" {
  bucket = google_storage_bucket.doors.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.door_uploader.email}"
}

# ---------------------------------------------------------------------------
# Container images
# ---------------------------------------------------------------------------

resource "google_artifact_registry_repository" "docker" {
  repository_id = "aocn"
  location      = var.region
  format        = "DOCKER"
  description   = "Container images for the Advent of Cloud Native API"

  # Untagged images accumulate with every deploy and are never rolled back to.
  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"
    condition {
      tag_state  = "UNTAGGED"
      older_than = "604800s" # 7 days
    }
  }

  depends_on = [google_project_service.enabled]
}

# ---------------------------------------------------------------------------
# Identities
# ---------------------------------------------------------------------------

# Four identities, one per job, so no credential can do more than its job needs:
# the service at runtime, app deploys, infra plans, infra applies.

# The only identity that can read secrets.
resource "google_service_account" "api_runtime" {
  account_id   = "aocn-api"
  display_name = "Advent of Cloud Native API (Cloud Run runtime)"

  depends_on = [google_project_service.enabled]
}

# Pushes images and rolls out revisions. Cannot touch infra, and holds no secret
# access of its own — though whatever it deploys runs as api_runtime, which does.
# That is inherent to continuous deployment: the boundary is who can reach this
# identity, not what it can read.
resource "google_service_account" "deployer" {
  account_id   = "aocn-deployer"
  display_name = "Advent of Cloud Native app deploys (GitHub Actions)"

  depends_on = [google_project_service.enabled]
}

# Read-only. Used by pull requests, which only need to plan.
resource "google_service_account" "infra_plan" {
  account_id   = "aocn-infra-plan"
  display_name = "Advent of Cloud Native infra plan, read-only (GitHub Actions)"

  depends_on = [google_project_service.enabled]
}

# Applies infra. Only reachable from main.
resource "google_service_account" "infra_apply" {
  account_id   = "aocn-infra-apply"
  display_name = "Advent of Cloud Native infra apply (GitHub Actions)"

  depends_on = [google_project_service.enabled]
}

# Uploads doors to the doors bucket, from main in the challenges repository. Can do
# nothing else.
resource "google_service_account" "door_uploader" {
  account_id   = "aocn-door-uploader"
  display_name = "Advent of Cloud Native door uploads (GitHub Actions, aocn-challenges)"

  depends_on = [google_project_service.enabled]
}

# --- app deploy permissions -------------------------------------------------

resource "google_artifact_registry_repository_iam_member" "deployer_push" {
  location   = google_artifact_registry_repository.docker.location
  repository = google_artifact_registry_repository.docker.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.deployer.email}"
}

# run.developer, not run.admin: enough to deploy revisions, not enough to change
# a service's IAM policy and make it public.
resource "google_project_iam_member" "deployer_run" {
  project = var.project_id
  role    = "roles/run.developer"
  member  = "serviceAccount:${google_service_account.deployer.email}"
}

# Deploying a service that runs as api_runtime counts as acting as it, so this is
# granted on that account rather than project-wide.
resource "google_service_account_iam_member" "deployer_acts_as_runtime" {
  service_account_id = google_service_account.api_runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.deployer.email}"
}

# Creating a service that runs as api_runtime counts as acting as it. serviceAccountAdmin
# is not enough: it can administer service accounts without being allowed to use one.
resource "google_service_account_iam_member" "infra_apply_acts_as_runtime" {
  service_account_id = google_service_account.api_runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.infra_apply.email}"
}

# --- infra permissions ------------------------------------------------------

# Project-wide read is enough to plan. Plans run with -lock=false, because writing
# a lock would need write access to the state bucket and defeat the point.
#
# securityReviewer is there because refreshing an *_iam_member resource makes the
# provider call getIamPolicy on whatever it is attached to, and roles/viewer does
# not grant that for every resource type — buckets in particular. Reading state is
# not enough; a plan has to be able to read the policies it manages.
resource "google_project_iam_member" "infra_plan" {
  for_each = toset([
    "roles/viewer",
    "roles/iam.securityReviewer",
    "roles/iam.workloadIdentityPoolViewer",
  ])

  project = var.project_id
  role    = each.key
  member  = "serviceAccount:${google_service_account.infra_plan.email}"
}

resource "google_storage_bucket_iam_member" "infra_plan_reads_state" {
  bucket = google_storage_bucket.state.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.infra_plan.email}"
}

# Scoped to the resource types this configuration manages rather than owner. That
# bounds what a wrong plan can damage; it is not a containment boundary, because
# projectIamAdmin can grant any role to any principal. Treat this as owner-equivalent
# once reached — what actually protects it is the binding below, which lets only
# refs/heads/main assume it.
resource "google_project_iam_member" "infra_apply" {
  for_each = toset([
    "roles/artifactregistry.admin",
    "roles/iam.serviceAccountAdmin",
    "roles/iam.workloadIdentityPoolAdmin",
    "roles/resourcemanager.projectIamAdmin",
    "roles/run.admin",
    "roles/secretmanager.admin",
    "roles/serviceusage.serviceUsageAdmin",
    "roles/storage.admin",
  ])

  project = var.project_id
  role    = each.key
  member  = "serviceAccount:${google_service_account.infra_apply.email}"
}

# --- Workload Identity Federation -------------------------------------------

# Lets GitHub Actions authenticate with its OIDC token, so there is no service
# account key to leak.
resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  display_name              = "GitHub Actions"

  depends_on = [google_project_service.enabled]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub Actions OIDC"

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }

  attribute_mapping = {
    "google.subject"             = "assertion.sub"
    "attribute.repository"       = "assertion.repository"
    "attribute.repository_id"    = "assertion.repository_id"
    "attribute.repository_owner" = "assertion.repository_owner"
    "attribute.ref"              = "assertion.ref"
  }

  # Without this, a workflow in any GitHub repository could present a valid token
  # to this provider.
  attribute_condition = "assertion.repository_id == \"${var.github_repository_id}\""
}

# Planning is read-only, so any workflow run in this repository may do it.
resource "google_service_account_iam_member" "github_plans" {
  service_account_id = google_service_account.infra_plan.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository_id/${var.github_repository_id}"
}

# Anything that can change something is scoped by ref, not by repository.
#
# A pull request runs the workflow file from its own head, so it can rewrite the
# workflow to ask for these identities. It cannot change the ref its OIDC token is
# issued for: pull request runs get refs/pull/N/merge. Scoping here rather than in
# the workflow puts the boundary somewhere a pull request author cannot edit.
#
# The provider's attribute_condition already restricts tokens to this repository,
# so matching on ref alone still means "this repository, on main".
resource "google_service_account_iam_member" "github_main_deploys" {
  for_each = {
    deployer    = google_service_account.deployer.name
    infra_apply = google_service_account.infra_apply.name
  }

  service_account_id = each.value
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.ref/refs/heads/main"
}

# --- Workload Identity Federation for the challenges repository -------------

# A pool of its own rather than a second repository in the one above. The bindings there
# trust refs/heads/main on its own, because that pool only admits this repository; admitting
# a second one would let its main branch deploy the API too.
resource "google_iam_workload_identity_pool" "github_challenges" {
  workload_identity_pool_id = "github-challenges"
  display_name              = "GitHub Actions (aocn-challenges)"

  depends_on = [google_project_service.enabled]
}

resource "google_iam_workload_identity_pool_provider" "github_challenges" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github_challenges.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub Actions OIDC"

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }

  attribute_mapping = {
    "google.subject"          = "assertion.sub"
    "attribute.repository_id" = "assertion.repository_id"
    "attribute.ref"           = "assertion.ref"
  }

  attribute_condition = "assertion.repository_id == \"${var.challenges_repository_id}\""
}

# Only main uploads: merging is the decision to publish a door. Pull requests get
# refs/pull/N/merge and can't reach it, whatever their workflow file says.
resource "google_service_account_iam_member" "github_challenges_main_uploads" {
  service_account_id = google_service_account.door_uploader.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github_challenges.name}/attribute.ref/refs/heads/main"
}

# ---------------------------------------------------------------------------
# Secrets
# ---------------------------------------------------------------------------

# Containers only: anything OpenTofu knows ends up in state, so versions are
# added out of band.
resource "google_secret_manager_secret" "secrets" {
  for_each = toset(local.secrets)

  secret_id = each.value

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

# Only the running service reads secrets; the deployer does not.
#
# for_each iterates the static list rather than google_secret_manager_secret.secrets:
# keys taken from a resource that does not exist yet are unknown at plan time.
resource "google_secret_manager_secret_iam_member" "api_runtime_reads" {
  for_each = toset(local.secrets)

  secret_id = google_secret_manager_secret.secrets[each.key].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.api_runtime.email}"
}

# ---------------------------------------------------------------------------
# The service
# ---------------------------------------------------------------------------

# What the service is stays here; what it runs does not. A deploy changes only the
# image, so that one field is ignored and the two never revert each other.
resource "google_cloud_run_v2_service" "api" {
  name     = "aocn-api"
  location = var.region

  # Reached over the public internet: the Netlify /api/* proxy is an ordinary HTTPS
  # client, not something inside this project.
  ingress = "INGRESS_TRAFFIC_ALL"

  # This file describes the service completely, so a destroy is recoverable. Blocking
  # one silently is worse than the mistake it would prevent.
  deletion_protection = false

  template {
    service_account = google_service_account.api_runtime.email

    scaling {
      # Nothing running costs nothing. Cold starts are fine for a calendar that opens
      # a door every other day; this is the first number to raise if they are not.
      min_instance_count = 0
      max_instance_count = 3
    }

    containers {
      # Replaced by the first deploy. ignore_changes below is what stops every later
      # apply from putting it back.
      image = "us-docker.pkg.dev/cloudrun/container/hello"

      env {
        name  = "DOORS_BUCKET"
        value = google_storage_bucket.doors.name
      }

      # Secrets reach the API as environment variables, which config.yaml expands.
      # "latest" is resolved when a revision starts, so a new secret version takes
      # effect with the next deploy, not before.
      dynamic "env" {
        for_each = {
          DATABASE_URL          = "database-url"
          RESEND_API_KEY        = "resend-api-key"
          SUBSCRIBE_SIGNING_KEY = "subscribe-signing-key"
        }

        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.secrets[env.value].secret_id
              version = "latest"
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].containers[0].image]
  }

  # A revision that reads secrets only starts once the runtime account may read them.
  depends_on = [
    google_project_service.enabled,
    google_secret_manager_secret_iam_member.api_runtime_reads,
  ]
}

# Public, because the Netlify proxy forwards anonymous visitors and there is no
# caller identity for Cloud Run to check. Deciding who may do what is the API's job.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.api.name
  location = google_cloud_run_v2_service.api.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
