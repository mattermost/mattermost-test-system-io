# Deployment Guide

## Overview

```
Developer opens PR → CI runs on PR (auto)
        │
        │ All checks pass + approved review
        ▼
  PR merged to main → CI runs on push (auto)
        │
        │ Quality checks: fmt, lint, typecheck, test, Docker build
        ▼
  ┌───────────┐
  │  Staging  │  Manual trigger → build + push beta image → deploy
  │  (manual) │  Migration path: deploy latest release → then beta
  └─────┬─────┘
        │ Validated in staging
        ▼
  ┌───────────┐
  │ Production│  Manual trigger → retag beta as release (no rebuild) → deploy
  │  (manual) │  Requires approval from designated reviewer
  └───────────┘
```

**No direct pushes to `main`.** All changes go through pull requests with required reviews and passing CI checks (branch protection enforced).

## Environments

| Environment | URL | Trigger |
|-------------|-----|---------|
| Staging | `https://staging-test-io.test.mattermost.com` | Manual (`workflow_dispatch`, no input) |
| Production | `https://test-io.test.mattermost.com` | Manual (`workflow_dispatch`, input: beta tag) |

## Deploy to Staging

### When to deploy

After a PR is merged to `main` and CI passes on the merge commit.

### How to deploy

1. Go to **Actions** → **Deploy Staging**
2. Click **Run workflow**
3. No input needed — deploys the current `main` branch

### What happens

```
1. check-concurrent    → Rejects if another staging deploy is running
2. build-and-tag       → Reads version from Cargo.toml
                        → Computes beta tag: {version}-{short_sha}.beta
                        → Builds Docker image and pushes to Docker Hub
                        → Creates GitHub prerelease with the beta tag
3. deploy              → Restarts PostgreSQL container (fresh database)
                        → If a previous release exists:
                            → Deploys latest release version first
                            → Waits for stable
                            → Then deploys beta version (exercises migrations)
                        → If no previous release:
                            → Deploys beta directly
                        → Waits for ECS service to stabilize
                        → Health check: curl $APP_URL/api/v1/ready
```

### Version tag format

```
{version}-{short_sha}.beta
Example: 0.1.0-abcdefg.beta
```

- Version is read from `apps/server/Cargo.toml`
- Short SHA is the first 7 characters of the commit hash
- No `v` prefix

### What is created

- Docker Hub image: `mattermostdevelopment/mattermost-test-system-io:0.1.0-abcdefg.beta`
- GitHub prerelease: `0.1.0-abcdefg.beta`
- Git tag: `0.1.0-abcdefg.beta`

## Promote to Production

### When to deploy

After validating the staging deployment (checking the staging URL, running tests against it, etc.).

### How to deploy

1. Go to **Actions** → **Deploy Production**
2. Click **Run workflow**
3. Enter the **beta tag** to promote (e.g., `0.1.0-abcdefg.beta`)
4. A designated reviewer must **approve** the deployment (GitHub Environment protection)

### What happens

```
1. check-concurrent    → Rejects if another production deploy is running
2. validate-and-retag  → Validates beta tag exists as GitHub prerelease
                        → Validates beta Docker image exists in Docker Hub
                        → Extracts release version (strips -{sha}.beta suffix)
                        → Retags image as release version + latest (NO rebuild)
                        → Creates GitHub release (not prerelease) with release tag
3. deploy              → Updates ECS service with release version image
                        → Waits for ECS service to stabilize
                        → If health check fails: ECS circuit breaker auto-rolls back
                        → Health check: curl $APP_URL/api/v1/ready
```

### Key: No rebuild

The production deployment does **not** rebuild the Docker image. It retags the exact same image that was tested in staging:

```
docker buildx imagetools create \
  --tag mattermostdevelopment/mattermost-test-system-io:0.1.0 \
  --tag mattermostdevelopment/mattermost-test-system-io:latest \
  mattermostdevelopment/mattermost-test-system-io:0.1.0-abcdefg.beta
```

This guarantees 100% artifact parity between staging and production.

### What is created

- Docker Hub image: `mattermostdevelopment/mattermost-test-system-io:0.1.0` + `:latest`
- GitHub release: `0.1.0`
- Git tag: `0.1.0`

## Deployment Flow Example

```bash
# 1. PR approved + merged to main → CI runs automatically

# 2. Deploy to staging
#    Actions → Deploy Staging → Run workflow
#    Creates: 0.1.0-abc1234.beta

# 3. Validate staging
curl https://staging-test-io.test.mattermost.com/api/v1/ready
# Run integration tests against staging URL...

# 4. Promote to production
#    Actions → Deploy Production → Run workflow
#    Input: 0.1.0-abc1234.beta
#    Approve in GitHub Environment review
#    Creates: 0.1.0 (release)
```

## Rollback

### Production (automatic)

ECS deployment circuit breaker is enabled. If the new task fails health checks, ECS automatically rolls back to the previous task definition. No manual action needed.

### Production (manual)

Re-promote the previous beta tag:

1. Go to **Actions** → **Deploy Production**
2. Enter the **previous** beta tag (find it in GitHub Releases under prereleases)

### Staging

Staging is automatically reset on every deployment (fresh PostgreSQL container). Simply trigger a new staging deployment.

## Concurrent Deployments

Concurrent deployments to the same environment are **rejected** (not queued). If another deployment is running:

```
::error::Another staging deployment is already in progress.
Please wait for it to complete.
```

Wait for the active deployment to finish, then retry.

## Troubleshooting

### Health check fails after deployment

1. Check ECS service events:
   ```bash
   aws ecs describe-services --cluster $CLUSTER --services $SERVICE \
     --query 'services[0].events[:5]'
   ```
2. Check application logs:
   ```bash
   aws logs tail /ecs/mattermost-test-system-io-shared --follow
   ```
3. Verify environment variables (DATABASE_URL, API_KEY, S3_BUCKET)
4. ECS circuit breaker will auto-rollback if the task can't start

### Beta tag not found during production promotion

Verify the beta exists:
```bash
gh release view 0.1.0-abcdefg.beta
docker buildx imagetools inspect mattermostdevelopment/mattermost-test-system-io:0.1.0-abcdefg.beta
```

### No previous release for staging migration

On the first-ever staging deployment, there's no prior release to migrate from. The workflow logs a warning and deploys the beta directly. This is expected. After the first production release, subsequent staging deployments will run the migration path.

## Initial Setup (one-time)

These steps must be completed before the first deployment.

### 1. Deploy CDK infrastructure

Follow the [infra README](../infra/README.md) to configure `infra/.env`, bootstrap CDK, and deploy all stacks. This creates the OIDC provider and deploy role in AWS.

### 2. Get the deploy role ARN

After deploying the `SharedStack`, retrieve the role ARN:

```bash
cd infra
aws cloudformation describe-stacks \
  --stack-name SharedStack \
  --query 'Stacks[0].Outputs[?ExportName==`mattermost-test-system-io-deploy-role-arn`].OutputValue' \
  --output text
```

### 3. Configure GitHub repository

Go to your repo → **Settings**:

**Secrets** (Settings → Secrets and variables → Actions → Secrets tab):

| Secret | Value |
|--------|-------|
| `DOCKERHUB_DEV_USERNAME` | Docker Hub username |
| `DOCKERHUB_DEV_TOKEN` | Docker Hub scoped access token (push-only to `mattermostdevelopment/mattermost-test-system-io`) |

**Variables** (Settings → Secrets and variables → Actions → Variables tab):

| Variable | Value |
|----------|-------|
| `AWS_DEPLOY_ROLE_ARN` | The role ARN from step 2 (e.g., `arn:aws:iam::<account>:role/...`) |

**Environments** (Settings → Environments):

| Environment | Configuration |
|-------------|---------------|
| `staging` | Deployment branches: select `main` only |
| `production` | Deployment branches: select `main` only + add required reviewers |

**Branch protection** (Settings → Branches → Add rule for `main`):

- Require pull request reviews before merging
- Require status checks to pass before merging
- Do not allow bypassing the above settings
- Restrict force pushes and deletions

**Actions permissions** (Settings → Actions → General):

- Allow select actions and reusable workflows
- Allow: `actions/*`, `aws-actions/*`, `docker/*`, `dtolnay/*`

### 4. Verify OIDC connection

Trigger the staging deploy workflow. The `Configure AWS credentials via OIDC` step should succeed. If it fails:

- **"Not authorized to perform sts:AssumeRoleWithWebIdentity"** — check that the trust policy matches your exact repo name and the workflow runs from `main`
- **"No OpenIDConnect provider found"** — the `SharedStack` wasn't deployed or was deployed in a different region
- **"Audience not allowed"** — verify the OIDC provider has `sts.amazonaws.com` as an allowed audience

## Prerequisites

### GitHub Secrets

| Secret | Purpose |
|--------|---------|
| `DOCKERHUB_DEV_USERNAME` | Docker Hub login (scoped access token) |
| `DOCKERHUB_DEV_TOKEN` | Docker Hub push token (scoped to specific repo) |

### GitHub Variables

| Variable | Purpose |
|----------|---------|
| `AWS_DEPLOY_ROLE_ARN` | IAM role ARN for GitHub OIDC authentication |

### GitHub Environments

| Environment | Protection Rules |
|-------------|-----------------|
| `staging` | Branch restriction: `main` only |
| `production` | Branch restriction: `main` only + required reviewer approval |

### Required Repository Settings

- **Branch protection** on `main`: required PR reviews, required status checks, no force push
- **Actions permissions**: allow only verified creators + explicitly allowlisted actions
- **CODEOWNERS**: maintainer review required for `.github/workflows/`, `infra/`, `Dockerfile`

## Security

- All workflow `uses:` are SHA-pinned with version comments
- CI uses `pull_request` event (not `pull_request_target`) — fork PRs cannot access secrets
- Deploy workflows use `workflow_dispatch` — only users with write access can trigger
- Explicit `permissions` on every workflow — minimum required scopes
- Sensitive computed values masked with `::add-mask::`
- AWS credentials via OIDC — no long-lived keys stored
- Docker Hub token is scoped (push-only to specific repository)
