# Infrastructure — AWS CDK

AWS infrastructure for mattermost-test-system-io, defined as code using [AWS CDK](https://docs.aws.amazon.com/cdk/v2/guide/home.html) (TypeScript).

## Architecture

```
AWS Account
├── SharedStack
│   └── GitHub OIDC IAM Provider + Deploy Role
│
└── AppStack (shared VPC, ALB, ECS cluster — ~$168/mo)
    ├── VPC (public + private subnets, 2 AZs, 1 NAT gateway)
    ├── ALB (1 shared, HTTPS with ACM wildcard cert, host-based routing)
    ├── ECS Fargate Cluster (shared)
    ├── Cloud Map Namespace (mattermost-test-io.internal)
    │
    ├── VPC Flow Logs (CloudWatch, 30-day retention)
    ├── ALB Access Logs (S3, 90-day retention)
    │
    ├── Staging (staging-test-io.test.mattermost.com)
    │   ├── ECS App Service (0.5 vCPU, 1 GiB, 1 task, read-only root fs)
    │   ├── ECS PostgreSQL 18.1 (ephemeral, Cloud Map discovery, Secrets Manager password)
    │   └── S3 Bucket (uploads, enforceSSL)
    │
    └── Production (test-io.test.mattermost.com)
        ├── ECS App Service (1 vCPU, 2 GiB, 2 tasks, circuit breaker, read-only root fs)
        ├── RDS PostgreSQL 18 (encrypted, multi-AZ, deletion protection, Secrets Manager password)
        └── S3 Bucket (uploads, versioned, enforceSSL)
```

### Environment URLs

| Environment | URL |
|-------------|-----|
| Production | `https://test-io.test.mattermost.com` |
| Staging | `https://staging-test-io.test.mattermost.com` |

### Key Design Decisions

- **CDK over Terraform**: TypeScript (same as frontend), no external state management, CloudFormation auto-rollback.
- **Staging PostgreSQL as ECS containers**: Ephemeral, destroyed and recreated each deployment. Cheap and fast to reset.
- **Production PostgreSQL as RDS**: Persistent, encrypted at rest, multi-AZ, deletion protection, managed backups, Secrets Manager password.
- **Single staging environment (migration)**: Validates upgrade paths by deploying the latest release first, then the beta. Fresh installs are implicitly tested since staging starts with an empty database each deployment.
- **Shared VPC + ALB**: Staging and production share 1 VPC, 1 NAT gateway, and 1 ALB with host-based routing. Saves ~$115/mo vs separate infrastructure. Isolation via security groups.
- **OIDC for GitHub Actions**: No long-lived AWS credentials. Short-lived tokens per workflow run.

## Project Structure

```
infra/
├── bin/
│   └── app.ts                    # CDK app entry point (instantiates all stacks)
├── lib/
│   ├── config.ts                 # Typed environment config (staging, production, shared)
│   ├── constructs/
│   │   ├── networking.ts         # VPC, ALB, ACM, Route 53, security groups
│   │   ├── ecs_cluster.ts        # ECS cluster, CloudWatch logs, Cloud Map namespace
│   │   ├── ecs_app_service.ts    # App service (FargateService + ALB target group + listener rule)
│   │   ├── ecs_postgres.ts       # PostgreSQL ECS service with Cloud Map (staging)
│   │   ├── rds_postgres.ts       # RDS PostgreSQL with Secrets Manager (production)
│   │   ├── storage_bucket.ts     # S3 bucket (private, encrypted)
│   │   └── github_oidc.ts        # GitHub OIDC IAM provider + deploy role
│   └── stacks/
│       ├── shared_stack.ts       # OIDC provider (cross-environment)
│       └── app_stack.ts          # Unified stack: shared infra + staging + production
├── test/
│   └── app_stack.test.ts         # CDK assertion tests (shared + staging + production)
├── cdk.json                      # CDK app config + context values
├── tsconfig.json
├── vitest.config.ts
└── package.json
```

## Prerequisites

- Node.js 24+
- AWS CLI configured with credentials
- AWS CDK CLI: installed via `npx cdk` (included in devDependencies)

## Commands

```bash
npm install                # Install dependencies
npm test                   # Run CDK assertion tests (Vitest)
npm run lint               # Lint with oxlint
npm run format             # Format with oxfmt
npm run typecheck          # Type check with tsc
npm run cdk:synth          # Synthesize CloudFormation templates
npm run cdk:diff           # Diff against deployed stacks
npm run cdk:deploy         # Deploy all stacks (requires -c imageTag=...)
```

Or from the `infra/` directory using npm scripts:

```bash
cd infra
npm ci                     # Install dependencies
npm test                   # Run CDK tests
npm run cdk:synth          # Synthesize templates
npm run cdk:diff           # Diff against deployed
npm run cdk:deploy         # Deploy all stacks (requires -c imageTag=...)
```

> **Note**: `imageTag` is a required context variable for deploying app stacks. Pass it via `-c imageTag=0.1.0-abc1234.beta`.

## Initial Setup

### 1. Configure environment variables

Create `infra/.env` (gitignored) with your AWS account details:

```bash
# infra/.env

# AWS account ID (12-digit number)
CDK_DEFAULT_ACCOUNT=<your-aws-account-id>

# AWS region for deployment
CDK_DEFAULT_REGION=us-east-1

# Route 53 hosted zone ID for the domain
ROUTE53_ZONE_ID=<your-zone-id>
```

| Variable | Required | Description |
|----------|----------|-------------|
| `CDK_DEFAULT_ACCOUNT` | Yes | AWS account ID where stacks are deployed |
| `CDK_DEFAULT_REGION` | Yes | AWS region (e.g., `us-east-1`) |
| `ROUTE53_ZONE_ID` | Yes | Route 53 hosted zone ID for the domain |

These are loaded automatically via `dotenv` when CDK runs. Do **not** put AWS credentials (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`) in this file — use `aws configure` or AWS SSO instead.

### 2. Configure AWS credentials

CDK uses standard AWS credential resolution. Use one of:

```bash
# Option A: AWS CLI (stores in ~/.aws/credentials)
aws configure

# Option B: AWS SSO
aws sso login --profile your-profile
export AWS_PROFILE=your-profile

# Option C: Environment variables (temporary)
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
```

### 3. Install dependencies

```bash
cd infra
npm ci
```

### 4. Bootstrap CDK (one-time per account/region)

CDK bootstrap creates a CloudFormation stack (`CDKToolkit`) with S3 bucket, ECR repository, and IAM roles needed for deployments:

```bash
npx cdk bootstrap
```

This uses `CDK_DEFAULT_ACCOUNT` and `CDK_DEFAULT_REGION` from your `.env` file. You only need to run this once per account/region combination.

### 5. Verify synthesis

```bash
npx cdk synth
```

This should complete without errors and output to `cdk.out/`.

### 6. Deploy all stacks

```bash
npx cdk deploy --all --require-approval never -c imageTag=0.1.0-abc1234.beta
```

Deploy order is automatic based on stack dependencies: SharedStack -> NetworkingStack -> ProductionDataStack -> StagingAppStack/ProductionAppStack.

## Updating Infrastructure

### Modify a construct or stack

1. Edit the relevant file in `lib/constructs/` or `lib/stacks/`
2. Run `npm test` to verify CDK assertion tests pass
3. Run `npx cdk diff` to preview changes
4. Run `npx cdk deploy <StackName>` to apply

### Change environment configuration

Edit values in `lib/config.ts`:

| Config | File Location | What to Change |
|--------|--------------|----------------|
| CPU/memory | `lib/config.ts` | `cpu`, `memoryLimitMiB` |
| Instance count | `lib/config.ts` | `desiredCount` |
| RDS instance size | `lib/config.ts` | `dbInstanceClass` |
| PostgreSQL version | `lib/config.ts` | `postgresVersion` (staging), engine version in `rds_postgres.ts` (production) |
| Domain names | `lib/config.ts` | `subdomainNames`, `freshSubdomain`, `migrationSubdomain` |

### Upgrade CDK version

```bash
npm install aws-cdk-lib@latest aws-cdk@latest
npm test              # Verify nothing broke
npx cdk diff          # Preview changes from CDK upgrade
```

## Credentials & Secrets

### What is stored where

| Secret | Location | How It Gets There |
|--------|----------|-------------------|
| **AWS credentials** | Nowhere stored — OIDC generates per workflow run | GitHub OIDC + IAM trust policy |
| **RDS password** | AWS Secrets Manager | Auto-generated by CDK (`Credentials.fromGeneratedSecret`) |
| **Staging DB passwords** | AWS Secrets Manager | Auto-generated by CDK (`generateSecretString`) |
| **Docker Hub token** | GitHub repository secret `DOCKERHUB_TOKEN` | Manually created scoped access token |
| **GitHub token** | Automatic (`GITHUB_TOKEN`) | Per-run, 1-hour expiry, minimum scoped |

### What is NOT stored

- No AWS access keys in GitHub secrets
- No database passwords in code, config, or environment files
- No database passwords in plaintext ECS task definitions — injected at runtime via Secrets Manager
- No `.env` files committed (blocked by `.gitignore` + `.gitleaks.toml`)

### Rotating credentials

| Credential | Rotation |
|-----------|----------|
| AWS (OIDC) | Automatic — new token every workflow run |
| RDS password | Automatic — enable via `rdsInstance.addRotationSingleUser()` or manual rotation in Secrets Manager console |
| Docker Hub token | Manual — regenerate in Docker Hub, update GitHub secret |
| Staging DB passwords | Automatic — new container = new secret value on each deployment |

## Security Practices

### Network Isolation

- ECS tasks run in **private subnets** (no public IP)
- RDS is **not publicly accessible** (`publiclyAccessible: false`)
- S3 buckets have **all public access blocked** (`BlockPublicAccess.BLOCK_ALL`) with **enforceSSL**
- ALB is the only internet-facing resource, in public subnets, with `dropInvalidHeaderFields` enabled
- Security groups follow **least privilege**: ALB -> app (port 8080), app -> database (port 5432)
- **VPC Flow Logs** enabled (all traffic, CloudWatch, 30-day retention)
- **ALB Access Logs** enabled (S3, 90-day retention)

### IAM

- GitHub Actions authenticates via **OIDC** — no long-lived keys
- OIDC trust policy restricted to `environment:staging` and `environment:production` claims only
- Deploy role has scoped ECS permissions (`DescribeServices`, `UpdateService` on project services only)
- `RegisterTaskDefinition` restricted by `ecs:task-definition-family` condition
- `iam:PassRole` restricted to project roles and `ecs-tasks.amazonaws.com` service only
- CDK bootstrap roles handle resource creation via CloudFormation
- Session duration limited to 1 hour

### Code & Repository

- All infrastructure changes go through **pull requests** with CODEOWNERS review
- `.gitleaks.toml` scans for accidental credential commits
- `.gitignore` excludes `.env`, `*.pem`, `*.key`, `credentials.json`
- `cdk.out/` is gitignored (contains synthesized templates with resolved values)
- GitHub Actions SHA-pinned to prevent supply chain attacks

### Container Security

- **Read-only root filesystem** on all app containers (`readonlyRootFilesystem: true`)
- **ECS Exec disabled** (`enableExecuteCommand: false`) — no shell access to running containers
- **Non-root user** in Dockerfile (`appuser`)
- **Deployment circuit breaker** with auto-rollback on health check failure

### CDK-Specific

- `RemovalPolicy.RETAIN` on production resources (RDS, S3) — prevents accidental deletion
- `RemovalPolicy.DESTROY` on staging resources — easy cleanup
- `imageTag` is a **required** context variable — no `latest` fallback to prevent accidental deployments
- Production RDS has **deletion protection** enabled as an additional guard
- `skipLibCheck: true` in tsconfig — avoids type conflicts from CDK internal types
- Test files excluded from `tsc` compilation — Vitest handles them separately

## Destroying Infrastructure

### Destroy all stacks

```bash
npx cdk destroy --all --force
```

CDK handles destroy ordering automatically (AppStack first, then SharedStack).

### Destroy a specific stack

```bash
npx cdk destroy AppStack
npx cdk destroy SharedStack
```

Always destroy `AppStack` before `SharedStack` — AppStack depends on SharedStack's OIDC role.

### What gets deleted vs retained

| Resource | Removal Policy | On Destroy |
|----------|---------------|------------|
| Staging S3 bucket | DESTROY | Emptied and deleted |
| Staging PostgreSQL (ECS) | DESTROY | Container stopped and removed |
| Staging DB secret | DESTROY | Deleted |
| VPC, ALB, ECS cluster | DESTROY | Deleted |
| OIDC provider, IAM roles | DESTROY | Deleted |
| **Production S3 bucket** | **RETAIN** | **Kept in AWS (orphaned)** |
| **Production RDS** | **RETAIN** | **Kept in AWS (orphaned)** |
| **Production RDS secret** | **RETAIN** | **Kept in AWS (orphaned)** |

Production resources use `RemovalPolicy.RETAIN` to prevent accidental data loss. CloudFormation removes them from the stack but does **not** delete the actual AWS resource.

### Clean up retained resources

After `cdk destroy`, manually delete orphaned production resources if no longer needed:

```bash
# Delete RDS instance
aws rds delete-db-instance \
  --db-instance-identifier <instance-id> \
  --skip-final-snapshot

# Delete S3 bucket (must empty first)
aws s3 rb s3://<bucket-name> --force

# Delete Secrets Manager secret
aws secretsmanager delete-secret \
  --secret-id <secret-arn> \
  --force-delete-without-recovery
```

### Remove CDK bootstrap

To fully clean up the AWS account (removes the CDKToolkit CloudFormation stack, S3 assets bucket, and ECR repository):

```bash
aws cloudformation delete-stack --stack-name CDKToolkit
```

## Troubleshooting

### `cdk synth` fails

- Check `route53ZoneId` is set in `lib/config.ts` or passed via `-c route53ZoneId=...`
- Run `npm install` to ensure dependencies are up to date
- Run `npx tsc --noEmit` to check for TypeScript errors (excluding test files)

### Stack stuck in `UPDATE_ROLLBACK_FAILED`

```bash
aws cloudformation continue-update-rollback --stack-name <StackName>
```

### CDK bootstrap not found

```bash
npx cdk bootstrap aws://ACCOUNT_ID/REGION
```

### ALB name too long

ALB names are limited to 32 characters. The construct uses `tsio-{environment}-alb` to stay within limits.
