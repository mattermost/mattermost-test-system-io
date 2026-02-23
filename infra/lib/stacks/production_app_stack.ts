import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as elbv2 from "aws-cdk-lib/aws-elasticloadbalancingv2";
import * as rds from "aws-cdk-lib/aws-rds";
import * as route53 from "aws-cdk-lib/aws-route53";
import * as s3 from "aws-cdk-lib/aws-s3";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import { Construct } from "constructs";

import { AppConfig } from "../config";
import { EcsAppService } from "../constructs/ecs_app_service";
import { EcsCluster } from "../constructs/ecs_cluster";

export interface ProductionAppStackProps extends cdk.StackProps {
  readonly config: AppConfig;
  readonly vpc: ec2.Vpc;
  readonly alb: elbv2.ApplicationLoadBalancer;
  readonly httpsListener: elbv2.ApplicationListener;
  readonly appSecurityGroup: ec2.SecurityGroup;
  readonly hostedZone: route53.IHostedZone;
  readonly rdsInstance: rds.DatabaseInstance;
  readonly rdsSecret: secretsmanager.ISecret;
  readonly databaseUrl: string;
  readonly bucket: s3.Bucket;
}

export class ProductionAppStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: ProductionAppStackProps) {
    super(scope, id, props);

    const { config } = props;
    const imageTag = this.node.tryGetContext("imageTag") ?? "latest";

    const ecsCluster = new EcsCluster(this, "EcsCluster", {
      environment: "production",
      projectName: config.projectName,
      vpc: props.vpc,
      enableServiceDiscovery: false,
    });

    const appService = new EcsAppService(this, "AppService", {
      environment: "production",
      projectName: config.projectName,
      serviceName: config.production.subdomain,
      cluster: ecsCluster.cluster,
      imageTag,
      desiredCount: config.production.desiredCount,
      cpu: config.production.cpu,
      memoryLimitMiB: config.production.memoryLimitMiB,
      httpsListener: props.httpsListener,
      alb: props.alb,
      vpc: props.vpc,
      appSecurityGroup: props.appSecurityGroup,
      domainName: config.domainName,
      hostedZone: props.hostedZone,
      listenerPriority: 200,
      minimumHealthyPercent: config.production.minimumHealthyPercent,
      maximumPercent: config.production.maximumPercent,
      environmentVariables: {
        RUST_ENV: "production",
        TSIO_DB_URL: props.databaseUrl,
        TSIO_S3_BUCKET: props.bucket.bucketName,
      },
      healthCheckGracePeriod: cdk.Duration.seconds(300),
    });

    props.rdsSecret.grantRead(appService.taskDefinition.taskRole);
    props.bucket.grantReadWrite(appService.taskDefinition.taskRole);
  }
}
