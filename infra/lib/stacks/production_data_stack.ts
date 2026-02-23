import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as rds from "aws-cdk-lib/aws-rds";
import * as s3 from "aws-cdk-lib/aws-s3";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import { Construct } from "constructs";

import { ProductionEnvConfig } from "../config";
import { RdsPostgres } from "../constructs/rds_postgres";
import { StorageBucket } from "../constructs/storage_bucket";

export interface ProductionDataStackProps extends cdk.StackProps {
  readonly projectName: string;
  readonly vpc: ec2.IVpc;
  readonly appSecurityGroup: ec2.ISecurityGroup;
  readonly production: ProductionEnvConfig;
}

export class ProductionDataStack extends cdk.Stack {
  public readonly rdsInstance: rds.DatabaseInstance;
  public readonly rdsSecret: secretsmanager.ISecret;
  public readonly databaseUrl: string;
  public readonly bucket: s3.Bucket;

  constructor(scope: Construct, id: string, props: ProductionDataStackProps) {
    super(scope, id, props);

    const { projectName, production } = props;

    // RDS PostgreSQL (persistent, RETAIN on delete)
    const rdsPostgres = new RdsPostgres(this, "Rds", {
      environment: "production",
      projectName,
      vpc: props.vpc,
      appSecurityGroup: props.appSecurityGroup,
      instanceClass: production.dbInstanceClass,
      allocatedStorage: production.dbAllocatedStorage,
      backupRetention: cdk.Duration.days(production.dbBackupRetentionDays),
      removalPolicy: cdk.RemovalPolicy.RETAIN,
    });

    this.rdsInstance = rdsPostgres.instance;
    this.rdsSecret = rdsPostgres.secret;
    this.databaseUrl = rdsPostgres.databaseUrl;

    // S3 bucket (persistent, RETAIN on delete, versioned)
    const storageBucket = new StorageBucket(this, "StorageBucket", {
      environment: "production",
      projectName,
      bucketSuffix: "production",
      removalPolicy: cdk.RemovalPolicy.RETAIN,
      versioned: true,
    });

    this.bucket = storageBucket.bucket;
  }
}
