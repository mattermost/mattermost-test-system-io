import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as ecs from "aws-cdk-lib/aws-ecs";
import * as elbv2 from "aws-cdk-lib/aws-elasticloadbalancingv2";
import * as route53 from "aws-cdk-lib/aws-route53";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import { Construct } from "constructs";

import { AppConfig } from "../config";
import { EcsAppService } from "../constructs/ecs_app_service";
import { EcsCluster } from "../constructs/ecs_cluster";
import { EcsPostgres } from "../constructs/ecs_postgres";
import { StorageBucket } from "../constructs/storage_bucket";

export interface StagingAppStackProps extends cdk.StackProps {
  readonly config: AppConfig;
  readonly vpc: ec2.Vpc;
  readonly alb: elbv2.ApplicationLoadBalancer;
  readonly httpsListener: elbv2.ApplicationListener;
  readonly appSecurityGroup: ec2.SecurityGroup;
  readonly hostedZone: route53.IHostedZone;
}

export class StagingAppStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: StagingAppStackProps) {
    super(scope, id, props);

    const { config } = props;
    const imageTag = this.node.tryGetContext("imageTag");
    if (!imageTag) {
      throw new Error(
        "imageTag context variable is required (e.g., -c imageTag=0.1.0-abc1234.beta)",
      );
    }

    const ecsCluster = new EcsCluster(this, "EcsCluster", {
      environment: "staging",
      projectName: config.projectName,
      vpc: props.vpc,
    });

    const postgres = new EcsPostgres(this, "Postgres", {
      environment: "staging",
      projectName: config.projectName,
      serviceName: "postgres",
      cluster: ecsCluster.cluster,
      vpc: props.vpc,
      postgresVersion: config.staging.postgresVersion,
      ephemeralStorageGiB: config.staging.postgresEphemeralStorageGiB,
      namespace: ecsCluster.namespace!,
      appSecurityGroup: props.appSecurityGroup,
    });

    const bucket = new StorageBucket(this, "StorageBucket", {
      environment: "staging",
      projectName: config.projectName,
      bucketSuffix: "staging",
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: true,
    });

    // Session signing secret. Auto-generated so neither humans nor CI ever
    // see the material; ECS mounts it as an env var at container start.
    const sessionSecret = new secretsmanager.Secret(this, "SessionSecret", {
      secretName: `${config.projectName}-staging-session-secret`,
      description: "TSIO session signing secret (staging)",
      generateSecretString: {
        passwordLength: 64,
        excludePunctuation: false,
        includeSpace: false,
      },
    });

    // Admin key gates POST /api/v1/admin/oidc-policies (and any future admin-only
    // HTTP endpoints). Auto-generated; rotate via `aws secretsmanager
    // put-secret-value ... && aws ecs update-service --force-new-deployment`.
    const adminKey = new secretsmanager.Secret(this, "AdminKey", {
      secretName: `${config.projectName}-staging-admin-key`,
      description: "TSIO admin key (X-Admin-Key) — staging",
      generateSecretString: {
        passwordLength: 64,
        excludePunctuation: true,
        includeSpace: false,
      },
    });

    const appService = new EcsAppService(this, "AppService", {
      environment: "staging",
      projectName: config.projectName,
      serviceName: config.staging.subdomain,
      cluster: ecsCluster.cluster,
      imageTag,
      desiredCount: config.staging.desiredCount,
      cpu: config.staging.cpu,
      memoryLimitMiB: config.staging.memoryLimitMiB,
      httpsListener: props.httpsListener,
      alb: props.alb,
      vpc: props.vpc,
      appSecurityGroup: props.appSecurityGroup,
      domainName: config.domainName,
      hostedZone: props.hostedZone,
      listenerPriority: 100,
      minimumHealthyPercent: config.staging.minimumHealthyPercent,
      maximumPercent: config.staging.maximumPercent,
      environmentVariables: {
        TSIO_ENVIRONMENT: "staging",
        TSIO_DB_HOST: postgres.dbHost,
        TSIO_DB_PORT: postgres.dbPort,
        TSIO_DB_USER: postgres.dbUser,
        TSIO_DB_NAME: postgres.dbName,
        // Staging Postgres is an ephemeral ECS container inside the VPC;
        // require would fail SSL handshake.
        TSIO_DB_SSLMODE: "disable",
        TSIO_S3_BUCKET: bucket.bucket.bucketName,
        // Enforce GitHub Actions OIDC `aud` claim. Workflows MUST request this
        // exact audience or token validation fails.
        TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE: "mattermost-test-system-io",
        // Re-seed the org-wide CI policy on every deploy. Staging recreates
        // its Postgres task on each deploy, so the github_oidc_policies row
        // would otherwise have to be POSTed manually after every cdk deploy.
        // Format: comma-separated `pattern=role`. ON CONFLICT DO NOTHING.
        TSIO_BOOTSTRAP_OIDC_POLICIES: "mattermost/*=uploader",
      },
      secrets: {
        TSIO_DB_PASSWORD: ecs.Secret.fromSecretsManager(postgres.dbPasswordSecret),
        TSIO_SESSION_SECRET: ecs.Secret.fromSecretsManager(sessionSecret),
        TSIO_ADMIN_KEY: ecs.Secret.fromSecretsManager(adminKey),
      },
      healthCheckGracePeriod: cdk.Duration.seconds(300),
      dbReadinessCheck: {
        host: `postgres.${ecsCluster.namespace!.namespaceName}`,
        postgresImage: `postgres:${config.staging.postgresVersion}`,
      },
    });

    appService.service.node.addDependency(postgres.service);
    bucket.bucket.grantReadWrite(appService.taskDefinition.taskRole);
  }
}
