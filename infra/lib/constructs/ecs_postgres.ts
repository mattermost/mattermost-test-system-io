import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as ecs from "aws-cdk-lib/aws-ecs";
import * as logs from "aws-cdk-lib/aws-logs";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import * as servicediscovery from "aws-cdk-lib/aws-servicediscovery";
import { Construct } from "constructs";

export interface EcsPostgresProps {
  readonly environment: string;
  readonly projectName: string;
  readonly serviceName: string;
  readonly cluster: ecs.ICluster;
  readonly vpc: ec2.IVpc;
  readonly postgresVersion?: string;
  readonly cpu?: number;
  readonly memoryLimitMiB?: number;
  readonly ephemeralStorageGiB?: number;
  readonly namespace: servicediscovery.INamespace;
  readonly dbName?: string;
  readonly dbUser?: string;
  readonly dbPassword?: string;
  readonly dbPasswordSecret?: secretsmanager.ISecret;
  readonly appSecurityGroup: ec2.ISecurityGroup;
}

export class EcsPostgres extends Construct {
  public readonly service: ecs.FargateService;
  public readonly connectionEndpoint: string;
  public readonly databaseUrl: string;

  constructor(scope: Construct, id: string, props: EcsPostgresProps) {
    super(scope, id);

    const postgresVersion = props.postgresVersion ?? "18.1";
    const cpu = props.cpu ?? 256;
    const memoryLimitMiB = props.memoryLimitMiB ?? 512;
    const ephemeralStorageGiB = props.ephemeralStorageGiB ?? 30;
    const dbName = props.dbName ?? "tsio";
    const dbUser = props.dbUser ?? "tsio";
    const dbPassword = props.dbPassword ?? "tsio";
    const prefix = `${props.projectName}-${props.environment}`;

    if (!props.dbPassword && !props.dbPasswordSecret) {
      throw new Error("Either dbPassword or dbPasswordSecret must be provided");
    }

    // Security group allowing port 5432 from appSecurityGroup only
    const postgresSecurityGroup = new ec2.SecurityGroup(this, "PostgresSecurityGroup", {
      vpc: props.vpc,
      securityGroupName: `${prefix}-${props.serviceName}-pg-sg`,
      description: `Security group for ${props.serviceName} PostgreSQL`,
      allowAllOutbound: true,
    });
    postgresSecurityGroup.addIngressRule(
      ec2.Peer.securityGroupId(props.appSecurityGroup.securityGroupId),
      ec2.Port.tcp(5432),
      "Allow PostgreSQL access from app security group",
    );

    // Fargate task definition
    const taskDefinition = new ecs.FargateTaskDefinition(this, "TaskDefinition", {
      family: `${prefix}-${props.serviceName}`,
      cpu,
      memoryLimitMiB,
      ephemeralStorageGiB,
    });

    // Postgres container
    const containerSecrets: Record<string, ecs.Secret> = {};
    const containerEnv: Record<string, string> = {
      POSTGRES_DB: dbName,
      POSTGRES_USER: dbUser,
    };

    if (props.dbPasswordSecret) {
      containerSecrets.POSTGRES_PASSWORD = ecs.Secret.fromSecretsManager(props.dbPasswordSecret);
    } else {
      containerEnv.POSTGRES_PASSWORD = dbPassword;
    }

    const container = taskDefinition.addContainer("postgres", {
      containerName: "postgres",
      image: ecs.ContainerImage.fromRegistry(`postgres:${postgresVersion}`),
      environment: containerEnv,
      secrets: containerSecrets,
      logging: ecs.LogDrivers.awsLogs({
        streamPrefix: `${prefix}-${props.serviceName}`,
        logRetention: logs.RetentionDays.TWO_WEEKS,
      }),
      essential: true,
      healthCheck: {
        command: ["CMD-SHELL", `pg_isready -U ${dbUser} -d ${dbName} || exit 1`],
        interval: cdk.Duration.seconds(10),
        timeout: cdk.Duration.seconds(5),
        retries: 5,
        startPeriod: cdk.Duration.seconds(30),
      },
    });

    container.addPortMappings({
      containerPort: 5432,
      protocol: ecs.Protocol.TCP,
    });

    // Fargate service with Cloud Map service discovery
    this.service = new ecs.FargateService(this, "Service", {
      serviceName: `${prefix}-${props.serviceName}`,
      cluster: props.cluster,
      taskDefinition,
      desiredCount: 1,
      minHealthyPercent: 0,
      assignPublicIp: false,
      securityGroups: [postgresSecurityGroup],
      vpcSubnets: { subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS },
      cloudMapOptions: {
        name: props.serviceName,
        cloudMapNamespace: props.namespace,
        dnsRecordType: servicediscovery.DnsRecordType.A,
        dnsTtl: cdk.Duration.seconds(10),
      },
    });

    // Derive the Cloud Map DNS name for the service
    const namespaceName = props.namespace.namespaceName ?? "mattermost-test-io.internal";
    this.connectionEndpoint = `${props.serviceName}.${namespaceName}:5432`;
    this.databaseUrl = `postgres://${dbUser}:${dbPassword}@${props.serviceName}.${namespaceName}:5432/${dbName}`;
  }
}
