# Lessons Learned — music-generator

Project: AI-powered music generator (Go + Cloud Run + Firestore + OpenAI)
Date: 2026-04-14
Template: Cloud Run (gcp-cloudrun-template)

## Summary of Issues

| # | Issue | Severity | Who | Status |
|---|-------|----------|-----|--------|
| 1 | Terraform referenced non-existent Secret Manager secret `openai-api-key` (IAM lookup 403) | High | DevOps | Fixed in `f011d79` |
| 2 | First CI run failed because secret resource was declared "created out-of-band" but never actually created | High | DevOps | Fixed (same commit) |
| 3 | OpenAI API key had to be injected via `TF_VAR_openai_api_key` — not documented in template | Medium | DevOps | Fixed + documented here |
| 4 | Custom domain TLS still provisioning at end of QA (expected, but caused verification ambiguity) | Low | QA | Acknowledged — no action |

---

## Issue 1: Terraform referenced a Secret Manager secret that didn't exist

### What happened
CI run `24418403396` (commit `5439c23`) failed at `terraform apply` on the staging environment with:

```
Error: Error retrieving IAM policy for secretmanager secret
"projects/dfh-stage-id/secrets/openai-api-key":
googleapi: Error 403: Permission 'secretmanager.secrets.getIamPolicy'
denied on resource (or it may not exist).
```

The same failure repeated on production CI (`24418365495`) and an earlier run (`24418285440`).

### Root cause
`terraform/modules/*/main.tf` declared a `google_secret_manager_secret_iam_member` resource that granted the Cloud Run runtime SA access to a secret named `openai-api-key`. A comment stated the secret was "created out-of-band", but:

1. No documentation, runbook, or bootstrap script actually created the secret.
2. The GCP 403 was ambiguous — it read as a permission problem, but the real cause was that the secret did not exist, so the IAM lookup landed on a non-existent resource and GCP masked the 404 as 403.

The deploy SA therefore hit the wall on the very first `terraform apply`.

### How it was fixed (commit `f011d79`)
- Added `google_secret_manager_secret` and `google_secret_manager_secret_version` resources to Terraform for both `stage` and `prod` environments.
- Added `import` blocks so pre-existing secrets (if already created manually during investigation) are adopted into state instead of conflicting.
- Added a `variable "openai_api_key"` (sensitive) and wired it through modules.
- In the GitHub Actions workflow, exported `TF_VAR_openai_api_key="${{ secrets.OPENAI_API_KEY }}"` before the `terraform apply` step, so the value is never written to disk or logs.

### How to prevent it
1. **Never declare a Terraform resource that depends on out-of-band state without a bootstrap path.** Either fully own the resource in Terraform (preferred) or add a `terraform import` block from day one.
2. **When a 403 appears on a GCP IAM call, always check whether the target resource actually exists first** — GCP frequently reports 403 for missing resources when the caller lacks list permission. A quick `gcloud secrets describe <name>` saves hours of misdiagnosis.
3. **Template update**: the Cloud Run template should ship with a ready-made pattern for application secrets (variable → `google_secret_manager_secret` → `…_version` → IAM binding → Cloud Run env var), so projects that need an API key don't have to reinvent it and don't fall into the "created out-of-band" trap.

---

## Issue 2: Ambiguous "created out-of-band" comment in Terraform

### What happened
The Terraform module contained a comment like `# secret created out-of-band` next to the IAM binding. This was treated as authoritative by reviewers, so nobody questioned why the secret wasn't being created by the module.

### Root cause
A throwaway comment from initial scaffolding became load-bearing documentation. No issue, runbook, or README pointed to the actual creation procedure because there wasn't one.

### How to prevent it
- Comments that describe an out-of-module assumption must be paired with a link to the runbook or bootstrap script that creates it. If there is no such script, write one or bring the resource inside Terraform.
- `golangci-lint` can't catch this — add it to the Architect/DevOps review checklist: **every external dependency referenced by Terraform must be either (a) imported, (b) created by Terraform, or (c) documented with a bootstrap runbook link.**

---

## Issue 3: OpenAI API key injection pattern was not defined up-front

### What happened
The backend service needs an OpenAI API key at runtime. The initial architecture referred to "Secret Manager" without specifying how the key value gets into Secret Manager in the first place. This was only resolved during the CI fix by passing the key through `TF_VAR_openai_api_key` from the GitHub secret `OPENAI_API_KEY`.

### Root cause
ARCHITECTURE.md documented the runtime access pattern (Cloud Run reads the secret via env var) but not the provisioning pattern (how the value arrives in Secret Manager).

### How to prevent it
- ARCHITECTURE.md must cover the **full secret lifecycle**: origin (who owns the real key), storage (GitHub Actions secret name), injection (TF_VAR), persistence (Secret Manager resource), and consumption (Cloud Run env var).
- **Template update**: add a "Secrets" section stub to the Architect's ARCHITECTURE.md template.

---

## Issue 4: Custom domain TLS provisioning delay at QA time

### What happened
At QA verification, the Cloud Run default `run.app` URLs returned HTTP 200 on all endpoints for both staging and production. The custom domain, however, was still provisioning its managed TLS certificate (Cloud Run domain mappings typically take 15–30 minutes).

### Root cause
Not a bug — Google-managed certificates are asynchronous. This became a process issue because the QA handoff happened before the certificate was active, creating ambiguity about deployment completeness.

### How to prevent it
- Include a post-deploy wait step in the CI pipeline (or a manual checklist item) that polls the custom-domain URL until it returns HTTPS 200, with a sensible timeout (e.g., 30 min) and an informational (not failing) message if the cert isn't ready yet.
- QA report template should distinguish between "Cloud Run URL green" and "custom domain green" as separate line items.

---

## Recommendations for Future Projects

### Backend Developer
- Validate that all required environment variables are present at startup and fail fast with a clear message. This makes missing-secret misconfigurations obvious in Cloud Run logs instead of surfacing as runtime 500s.
- Keep OpenAI client calls behind an interface so tests can inject a fake without touching the network (already done here — keep doing it).

### Frontend Developer
- No issues on the frontend in this project. Continue shipping self-contained HTML templates with inline CSS/JS via Go's `embed` package.

### DevOps Engineer
- **Own every resource in Terraform** — or document the bootstrap explicitly with a runbook link. Never leave "created out-of-band" as a standalone comment.
- **Before merging a Terraform change, run `terraform plan` locally with the same SA the CI uses.** The 403-for-missing-resource surprise would have been caught immediately in a plan.
- Use `import` blocks liberally when adopting pre-existing resources — they're idempotent after the first apply and eliminate state/reality drift.
- Keep CI workflow Terraform version at **1.6+** (confirmed in this project).

### QA Engineer
- Split verification into **two phases**: (1) Cloud Run default URL smoke test — blocking; (2) custom domain TLS verification — non-blocking, report-only if cert is still provisioning.
- Always include the exact URLs tested and the HTTP status codes in the final QA report.

### Team Lead
- During the Architect's kickoff, ask explicitly: **"What external secrets does this service need, and who puts them into Secret Manager?"** Don't let this be implicit.
- When CI fails with a GCP permission error, delegate the diagnosis with the hypothesis **"it's probably a missing resource, not a missing permission"** — this collapses the debug loop.

### Template Update Recommendations (Cloud Run template)

1. **Add a first-class application-secret module** in `terraform/modules/` that takes a variable, creates the secret + version, binds IAM to the runtime SA, and exposes the secret name for the Cloud Run service env var. Consumers just set the variable and reference the module.
2. **Add an `ARCHITECTURE.md` Secrets section stub** that forces the Architect to document origin → storage → injection → consumption for every secret.
3. **Add a `gcloud` pre-flight check** in the CI workflow that runs before `terraform apply` and verifies that all externally-owned resources referenced by Terraform actually exist, with a clear error message if not.
4. **Document the `TF_VAR_*` pattern** in the template README for injecting GitHub Actions secrets into Terraform without writing them to `.tfvars` files.
5. **Add a custom-domain TLS wait step** to the smoke-test stage, non-failing on timeout, so the pipeline logs clearly show the cert status at deploy time.

---

## Project Outcome

- Staging: https://music-generator-stage-c3ugnmjtaa-uc.a.run.app — all checks green
- Production: https://music-generator-prod-bzq3iptspa-uc.a.run.app — all checks green
- Custom domain TLS: provisioning at end of QA (expected)
- Final CI run: `24418995924` (main) — success, 2m42s
- Total failed CI runs before green: 3
- Total commits: 6
