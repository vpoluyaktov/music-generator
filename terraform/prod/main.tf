terraform {
  required_version = ">= 1.6"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 5.0"
    }
  }
}

# -----------------------------------------------------------------------------
# Locals — environment-specific constants
# -----------------------------------------------------------------------------

locals {
  project_id              = "dfh-prod-id"
  region                  = "us-central1"
  environment             = "production"
  env_short               = "prod"
  service_name            = "music-generator-prod"
  runtime_sa_id           = "music-gen-prod"
  custom_domain           = "music-generator.demo.devops-for-hire.com"
  dns_project_id          = "dfh-ops-id"
  dns_zone_name           = "demo-devops-for-hire-com"
  firestore_database_name = "music-generator"
  firestore_location      = "nam5"
  openai_secret_id        = "openai-api-key"
  min_instances           = 0
  max_instances           = 5
  cpu_limit               = "1"
  memory_limit            = "512Mi"
  timeout                 = "60s"
}

variable "image_tag" {
  description = "Docker image tag to deploy"
  type        = string
  default     = "latest"
}

provider "google" {
  project = local.project_id
  region  = local.region
}

provider "google-beta" {
  project = local.project_id
  region  = local.region
}

# -----------------------------------------------------------------------------
# APIs
# -----------------------------------------------------------------------------

resource "google_project_service" "apis" {
  for_each = toset([
    "run.googleapis.com",
    "firestore.googleapis.com",
    "iam.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "dns.googleapis.com",
    "logging.googleapis.com",
    "secretmanager.googleapis.com",
    "artifactregistry.googleapis.com",
    "containerregistry.googleapis.com",
  ])

  project = local.project_id
  service = each.value

  disable_dependent_services = false
  disable_on_destroy         = false
}

# -----------------------------------------------------------------------------
# Runtime service account + IAM
# -----------------------------------------------------------------------------

resource "google_service_account" "runtime" {
  account_id   = local.runtime_sa_id
  display_name = "music-generator Runtime SA (${title(local.environment)})"
  project      = local.project_id

  depends_on = [google_project_service.apis]
}

resource "google_project_iam_member" "runtime_logging" {
  project = local.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_project_iam_member" "runtime_firestore" {
  project = local.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_project_iam_member" "runtime_secrets" {
  project = local.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.runtime.email}"
}

# Secret-level IAM binding for the openai-api-key secret (created out-of-band)
resource "google_secret_manager_secret_iam_member" "runtime_openai_key" {
  project   = local.project_id
  secret_id = local.openai_secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"

  depends_on = [google_project_service.apis]
}

# -----------------------------------------------------------------------------
# Firestore
# -----------------------------------------------------------------------------

resource "google_firestore_database" "main" {
  project     = local.project_id
  name        = local.firestore_database_name
  location_id = local.firestore_location
  type        = "FIRESTORE_NATIVE"

  depends_on = [google_project_service.apis]
}

# -----------------------------------------------------------------------------
# Cloud Run service
# -----------------------------------------------------------------------------

resource "google_cloud_run_v2_service" "app" {
  name     = local.service_name
  location = local.region
  project  = local.project_id

  template {
    service_account = google_service_account.runtime.email

    scaling {
      min_instance_count = local.min_instances
      max_instance_count = local.max_instances
    }

    containers {
      image = "gcr.io/${local.project_id}/music-generator:${var.image_tag}"

      ports {
        container_port = 8080
      }

      env {
        name  = "PORT"
        value = "8080"
      }

      env {
        name  = "ENVIRONMENT"
        value = local.environment
      }

      env {
        name  = "APP_VERSION"
        value = var.image_tag
      }

      env {
        name  = "GCP_PROJECT_ID"
        value = local.project_id
      }

      env {
        name  = "FIRESTORE_DATABASE"
        value = local.firestore_database_name
      }

      env {
        name = "OPENAI_API_KEY"
        value_source {
          secret_key_ref {
            secret  = local.openai_secret_id
            version = "latest"
          }
        }
      }

      resources {
        limits = {
          cpu    = local.cpu_limit
          memory = local.memory_limit
        }
        cpu_idle = true
      }
    }

    timeout = local.timeout
  }

  traffic {
    percent = 100
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
  }

  depends_on = [
    google_project_service.apis,
    google_firestore_database.main,
    google_project_iam_member.runtime_firestore,
    google_project_iam_member.runtime_secrets,
    google_secret_manager_secret_iam_member.runtime_openai_key,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  project  = google_cloud_run_v2_service.app.project
  location = google_cloud_run_v2_service.app.location
  name     = google_cloud_run_v2_service.app.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# -----------------------------------------------------------------------------
# DNS — CNAME in dfh-ops-id zone
# -----------------------------------------------------------------------------
# The Cloud Run custom domain mapping itself is a one-time out-of-band step:
#
#   gcloud beta run domain-mappings create \
#     --service=music-generator-prod \
#     --domain=music-generator.demo.devops-for-hire.com \
#     --region=us-central1 \
#     --project=dfh-prod-id

resource "google_dns_record_set" "cloud_run_cname" {
  project      = local.dns_project_id
  managed_zone = local.dns_zone_name
  name         = "${local.custom_domain}."
  type         = "CNAME"
  ttl          = 300
  rrdatas      = ["ghs.googlehosted.com."]
}

# -----------------------------------------------------------------------------
# Outputs
# -----------------------------------------------------------------------------

output "service_url" {
  description = "Cloud Run service URL"
  value       = google_cloud_run_v2_service.app.uri
}

output "custom_domain_url" {
  description = "Custom domain URL (requires Cloud Run domain mapping)"
  value       = "https://${local.custom_domain}"
}

output "runtime_sa_email" {
  description = "Runtime service account email"
  value       = google_service_account.runtime.email
}

output "project_id" {
  value = local.project_id
}

output "region" {
  value = local.region
}

output "firestore_database_name" {
  value = google_firestore_database.main.name
}
