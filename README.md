# music-generator

AI-assisted melody composition web app. You type a prompt (e.g., *"Create a 4 voices melody in Bach baroque style"*), the backend calls OpenAI GPT-4o to generate a melody in **ABC notation**, the result is stored in Firestore, and the browser renders the sheet music with [abcjs](https://www.abcjs.net/). You can edit the ABC text in place — the staff re-renders live — and save, duplicate, or delete melodies from a sidebar.

**Live URLs:**

| Environment | URL |
|-------------|-----|
| Staging | https://music-generator.stage.demo.devops-for-hire.com |
| Production | https://music-generator.demo.devops-for-hire.com |

> See [ARCHITECTURE.md](ARCHITECTURE.md) for the authoritative design reference: data models, Store interface, API contracts, Terraform resources, CI/CD pipeline, and edge cases.

---

## Stack at a Glance

- Go 1.22 (`net/http` with 1.22 ServeMux method+path routing)
- OpenAI `gpt-4o` via `github.com/sashabaranov/go-openai`
- Google Cloud Firestore (named database `music-generator`)
- Google Cloud Run (multi-environment) + Google Cloud DNS
- abcjs v6 loaded from CDN for score rendering
- Terraform **>= 1.6** for infrastructure-as-code
- GitHub Actions for CI/CD

---

## Local Development

### Prerequisites

- Go **1.22** or newer
- Docker (optional, for local container builds)
- `gcloud` CLI (only needed for talking to real Firestore locally)
- An OpenAI API key with access to `gpt-4o`

### Environment Variables

Create a `.env` file (gitignored) in the repo root, or `export` these in your shell:

```bash
export PORT=8080
export ENVIRONMENT=local
export APP_VERSION=dev
export OPENAI_API_KEY=sk-your-key-here

# Optional — only set these to talk to real Firestore from your laptop
export GCP_PROJECT_ID=dfh-stage-id
export FIRESTORE_DATABASE=music-generator
```

If `GCP_PROJECT_ID` is empty the Firestore client is not initialised and any endpoint that reads/writes melodies returns `503 Service Unavailable`. The UI still loads and the `/api/generate` endpoint still calls OpenAI, but nothing is persisted.

If `OPENAI_API_KEY` is empty, `POST /api/generate` returns `503 Service Unavailable`; the other endpoints continue to work.

### Authenticating to Firestore Locally

```bash
gcloud auth application-default login
gcloud config set project dfh-stage-id
```

The Go Firestore SDK picks up Application Default Credentials automatically.

### Run the Service

```bash
cd service
go run .
```

Open http://localhost:8080/.

### Running Tests

```bash
cd service
go test ./...
go vet ./...
golangci-lint run ./...
```

All three **must** pass before pushing; CI runs the same commands.

### Run in Docker Locally

```bash
docker build -t music-generator ./service
docker run --rm -p 8080:8080 \
  -e OPENAI_API_KEY="$OPENAI_API_KEY" \
  -e ENVIRONMENT=local \
  -e APP_VERSION=dev \
  music-generator
```

---

## Deployment

Deployment is **fully automated** via GitHub Actions. Do **not** deploy manually from a developer workstation.

| Git branch | Environment | GCP project | Triggers on |
|------------|-------------|-------------|-------------|
| `stage` | Staging | `dfh-stage-id` | every push |
| `main` | Production | `dfh-prod-id` | every push |

Pull requests against either branch run the `test` job only — no deployment.

### Pipeline Stages (in order)

1. **Test** — `go test ./...`, `go vet ./...`, `golangci-lint run ./...` (in `./service`)
2. **Compute version** — `<MAJOR.MINOR from VERSION>.<commit_count>` → `v1.0.42`
3. **Docker build** — multi-stage image, tagged `v<version>` and `latest`
4. **Push to GCR** — `gcr.io/<project>/music-generator:<tag>`
5. **Pre-deploy resource check** — fails fast if pre-existing GCP resources collide with Terraform state
6. **Terraform apply** — runs with `-var="image_tag=v<version>"` and the env-specific `.tfvars`
7. **Smoke test** — `curl --fail --retry 5 --retry-delay 10 "$URL/health"`

### Promoting from Stage to Production

```bash
git checkout stage
git pull
# verify the staging deploy looks good: https://music-generator.stage.demo.devops-for-hire.com

git checkout main
git merge stage --ff-only
git push origin main        # triggers the production deploy
```

### Bumping the Version

Edit `VERSION` (e.g., `1.0` → `1.1`) and commit. The patch number is computed by CI from the commit count, so the full version becomes `v1.1.<N>` automatically on the next deploy.

---

## One-Time Manual Setup

Most infrastructure is managed by Terraform. A few items are **deliberately manual** because they involve secrets, human verification, or cross-project IAM.

### 1. Create the OpenAI API key secret (per environment)

The OpenAI API key is stored in Secret Manager and referenced by the Cloud Run service. It is not stored in Terraform state or git.

```bash
# Staging
echo -n "sk-your-staging-key" | \
  gcloud secrets create openai-api-key \
    --project=dfh-stage-id \
    --data-file=- \
    --replication-policy=automatic

# Production
echo -n "sk-your-production-key" | \
  gcloud secrets create openai-api-key \
    --project=dfh-prod-id \
    --data-file=- \
    --replication-policy=automatic
```

To rotate, add a new version:

```bash
echo -n "sk-new-key" | \
  gcloud secrets versions add openai-api-key \
    --project=<project-id> --data-file=-
```

Cloud Run pulls the `latest` version on every new revision.

### 2. Create the Cloud Run domain mapping (per environment, one-time)

Terraform creates the DNS CNAME record automatically. The Cloud Run **domain mapping** must be created manually the first time:

```bash
# Staging
gcloud beta run domain-mappings create \
  --service=music-generator-stage \
  --domain=music-generator.stage.demo.devops-for-hire.com \
  --region=us-central1 \
  --project=dfh-stage-id

# Production
gcloud beta run domain-mappings create \
  --service=music-generator-prod \
  --domain=music-generator.demo.devops-for-hire.com \
  --region=us-central1 \
  --project=dfh-prod-id
```

**Prerequisite:** the calling identity must be a verified owner of `demo.devops-for-hire.com` in Google Search Console. The existing deploy service accounts in both GCP projects are already verified owners.

### 3. GitHub Secrets (one-time)

Set per-repo secrets so GitHub Actions can authenticate to GCP:

```bash
gh secret set GCP_STAGE_SA_KEY < /path/to/dfh-stage-deploy-sa.json
gh secret set GCP_PROD_SA_KEY  < /path/to/dfh-prod-deploy-sa.json
```

Both deploy service accounts pre-exist in their respective projects with the roles listed in ARCHITECTURE.md §8.

### 4. GCS state buckets (pre-existing)

`dfh-stage-tfstate` and `dfh-prod-tfstate` already exist and hold Terraform state under the `music-generator/state` prefix. No action required.

---

## Troubleshooting

### `/api/generate` returns 502 with "malformed ABC response"

OpenAI occasionally returns prose instead of ABC. The server strips ``` fences and checks for an `X:` header. If the response has no `X:` header after cleanup, the call fails with 502. Retry the request or reword the prompt to be more explicit (e.g., add *"in ABC notation"*).

### `/api/generate` returns 503 "OpenAI not configured"

The server started without `OPENAI_API_KEY`. Either set it in the environment (local dev) or confirm the Secret Manager secret exists and the Cloud Run service has `roles/secretmanager.secretAccessor` (runtime SA `music-gen-<env>@<project>.iam.gserviceaccount.com`).

### List/get/update endpoints return 503 "Store not configured"

`GCP_PROJECT_ID` is unset. In Cloud Run this is injected by Terraform automatically; locally you must `export GCP_PROJECT_ID=...`.

### Staff viewer (top of the page) renders nothing

The browser failed to load abcjs from `cdn.jsdelivr.net`. Check the browser devtools network panel. Common causes: corporate firewall blocking CDNs, ad blocker, or a stale SRI hash. The ABC editor and API continue to work even if the renderer is offline.

### CI fails at "Pre-existing resource detected"

The pre-deploy resource check found a Cloud Run service or Firestore database that Terraform does not know about. Either import it into Terraform state:

```bash
terraform -chdir=terraform import \
  -var-file=stage/stage.tfvars \
  google_cloud_run_v2_service.service \
  projects/dfh-stage-id/locations/us-central1/services/music-generator-stage
```

...or delete the orphan resource and let Terraform re-create it.

### Terraform apply fails with "variable not allowed in import block"

Your local Terraform is older than 1.6. Upgrade:

```bash
tfswitch 1.6.6   # or brew upgrade tflint
```

CI pins Terraform to **1.6.6**.

### Cloud Run domain mapping shows as "Pending" indefinitely

The DNS CNAME is wrong or Google has not verified domain ownership yet. Verify:

```bash
dig CNAME music-generator.stage.demo.devops-for-hire.com +short
# should return: ghs.googlehosted.com.
```

If correct, wait ~15 minutes for Google-managed SSL provisioning. If still pending after an hour, re-run `gcloud beta run domain-mappings describe ...` and inspect `status.conditions`.
