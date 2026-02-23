import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as ecs from "aws-cdk-lib/aws-ecs";
import * as elbv2 from "aws-cdk-lib/aws-elasticloadbalancingv2";
import * as route53 from "aws-cdk-lib/aws-route53";
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
        RUST_ENV: "production",
        TSIO_DB_HOST: postgres.dbHost,
        TSIO_DB_PORT: postgres.dbPort,
        TSIO_DB_USER: postgres.dbUser,
        TSIO_DB_NAME: postgres.dbName,
        TSIO_S3_BUCKET: bucket.bucket.bucketName,
      },
      secrets: {
        TSIO_DB_PASSWORD: ecs.Secret.fromSecretsManager(postgres.dbPasswordSecret),
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
