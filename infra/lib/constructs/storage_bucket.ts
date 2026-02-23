import * as cdk from "aws-cdk-lib";
import * as s3 from "aws-cdk-lib/aws-s3";
import { Construct } from "constructs";

export interface StorageBucketProps {
  readonly environment: string;
  readonly projectName: string;
  readonly bucketSuffix: string;
  readonly removalPolicy?: cdk.RemovalPolicy;
  readonly autoDeleteObjects?: boolean;
  readonly versioned?: boolean;
}

export class StorageBucket extends Construct {
  public readonly bucket: s3.Bucket;

  constructor(scope: Construct, id: string, props: StorageBucketProps) {
    super(scope, id);

    const removalPolicy = props.removalPolicy ?? cdk.RemovalPolicy.RETAIN;
    const autoDeleteObjects = props.autoDeleteObjects ?? false;
    const versioned = props.versioned ?? false;

    this.bucket = new s3.Bucket(this, "Bucket", {
      bucketName: `${props.projectName}-${props.bucketSuffix}-${props.environment}`,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      versioned,
      removalPolicy,
      autoDeleteObjects,
    });
  }
}
