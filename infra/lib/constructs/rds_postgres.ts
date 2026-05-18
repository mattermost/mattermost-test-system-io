import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as rds from "aws-cdk-lib/aws-rds";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import { Construct } from "constructs";

export interface RdsPostgresProps {
  readonly environment: string;
  readonly projectName: string;
  readonly vpc: ec2.IVpc;
  readonly appSecurityGroup: ec2.ISecurityGroup;
  readonly instanceClass?: ec2.InstanceType;
  readonly engineVersion?: rds.PostgresEngineVersion;
  readonly allocatedStorage?: number;
  readonly dbName?: string;
  readonly dbUsername?: string;
  readonly backupRetention?: cdk.Duration;
  readonly removalPolicy?: cdk.RemovalPolicy;
  readonly multiAz?: boolean;
  readonly deletionProtection?: boolean;
}

export class RdsPostgres extends Construct {
  public readonly instance: rds.DatabaseInstance;
  public readonly secret: secretsmanager.ISecret;
  public readonly endpoint: rds.Endpoint;
  public readonly dbName: string;
  public readonly dbUsername: string;

  constructor(scope: Construct, id: string, props: RdsPostgresProps) {
    super(scope, id);

    const instanceClass =
      props.instanceClass ?? ec2.InstanceType.of(ec2.InstanceClass.T4G, ec2.InstanceSize.MICRO);
    const engineVersion = props.engineVersion ?? rds.PostgresEngineVersion.VER_18_1;
    const allocatedStorage = props.allocatedStorage ?? 20;
    const dbName = props.dbName ?? "tsio";
    const dbUsername = props.dbUsername ?? "tsio";
    const backupRetention = props.backupRetention ?? cdk.Duration.days(7);
    const removalPolicy = props.removalPolicy ?? cdk.RemovalPolicy.RETAIN;
    const multiAz = props.multiAz ?? false;
    const deletionProtection = props.deletionProtection ?? false;

    const securityGroup = new ec2.SecurityGroup(this, "SecurityGroup", {
      vpc: props.vpc,
      description: `${props.projectName}-${props.environment} RDS PostgreSQL`,
      allowAllOutbound: false,
    });

    securityGroup.addIngressRule(
      props.appSecurityGroup,
      ec2.Port.tcp(5432),
      "Allow PostgreSQL access from app security group",
    );

    this.instance = new rds.DatabaseInstance(this, "Instance", {
      engine: rds.DatabaseInstanceEngine.postgres({
        version: engineVersion,
      }),
      instanceType: instanceClass,
      vpc: props.vpc,
      vpcSubnets: { subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS },
      securityGroups: [securityGroup],
      credentials: rds.Credentials.fromGeneratedSecret(dbUsername),
      databaseName: dbName,
      allocatedStorage,
      publiclyAccessible: false,
      storageEncrypted: true,
      multiAz,
      deletionProtection,
      backupRetention,
      removalPolicy,
    });

    this.secret = this.instance.secret!;
    this.endpoint = this.instance.instanceEndpoint;
    this.dbName = dbName;
    this.dbUsername = dbUsername;
  }
}
