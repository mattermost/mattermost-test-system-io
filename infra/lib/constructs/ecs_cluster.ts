import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as ecs from "aws-cdk-lib/aws-ecs";
import * as logs from "aws-cdk-lib/aws-logs";
import * as servicediscovery from "aws-cdk-lib/aws-servicediscovery";
import { Construct } from "constructs";

export interface EcsClusterProps {
  readonly environment: string;
  readonly projectName: string;
  readonly vpc: ec2.IVpc;
  readonly logRetentionDays?: number;
  readonly enableServiceDiscovery?: boolean;
  readonly cloudMapNamespaceName?: string;
}

export class EcsCluster extends Construct {
  public readonly cluster: ecs.Cluster;
  public readonly logGroup: logs.LogGroup;
  public readonly namespace?: servicediscovery.PrivateDnsNamespace;

  constructor(scope: Construct, id: string, props: EcsClusterProps) {
    super(scope, id);

    const logRetention = (props.logRetentionDays ?? 30) as logs.RetentionDays;
    const prefix = `${props.projectName}-${props.environment}`;

    // CloudWatch log group with configurable retention
    this.logGroup = new logs.LogGroup(this, "LogGroup", {
      logGroupName: `/ecs/${prefix}`,
      retention: logRetention,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    });

    // ECS Cluster with container insights enabled
    this.cluster = new ecs.Cluster(this, "Cluster", {
      clusterName: `${prefix}-cluster`,
      vpc: props.vpc,
      containerInsightsV2: ecs.ContainerInsights.ENABLED,
    });

    // Cloud Map PrivateDnsNamespace (only when service discovery is needed)
    if (props.enableServiceDiscovery !== false) {
      const namespaceName = props.cloudMapNamespaceName ?? "mattermost-test-io.internal";
      this.namespace = new servicediscovery.PrivateDnsNamespace(this, "Namespace", {
        name: namespaceName,
        vpc: props.vpc,
        description: `Service discovery namespace for ${prefix}`,
      });
    }
  }
}
