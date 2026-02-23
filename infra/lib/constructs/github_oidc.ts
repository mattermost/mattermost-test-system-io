import * as cdk from "aws-cdk-lib";
import * as iam from "aws-cdk-lib/aws-iam";
import { Construct } from "constructs";

export interface GithubOidcProps {
  readonly projectName: string;
  readonly githubOrg: string;
  readonly githubRepo: string;
}

export class GithubOidc extends Construct {
  public readonly provider: iam.OpenIdConnectProvider;
  public readonly deployRole: iam.Role;

  constructor(scope: Construct, id: string, props: GithubOidcProps) {
    super(scope, id);

    this.provider = new iam.OpenIdConnectProvider(this, "Provider", {
      url: "https://token.actions.githubusercontent.com",
      clientIds: ["sts.amazonaws.com"],
    });

    this.deployRole = new iam.Role(this, "DeployRole", {
      roleName: `${props.projectName}-github-deploy`,
      assumedBy: new iam.WebIdentityPrincipal(this.provider.openIdConnectProviderArn, {
        StringLike: {
          "token.actions.githubusercontent.com:sub": [
            `repo:${props.githubOrg}/${props.githubRepo}:environment:staging`,
            `repo:${props.githubOrg}/${props.githubRepo}:environment:production`,
          ],
        },
        StringEquals: {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
        },
      }),
      maxSessionDuration: cdk.Duration.hours(1),
    });

    // CDK bootstrap role assumption (required for cdk deploy)
    this.deployRole.addToPolicy(
      new iam.PolicyStatement({
        sid: "CdkBootstrapRoles",
        effect: iam.Effect.ALLOW,
        actions: ["sts:AssumeRole"],
        resources: [`arn:${cdk.Aws.PARTITION}:iam::${cdk.Aws.ACCOUNT_ID}:role/cdk-*`],
      }),
    );

    // ECS service management (scoped to project clusters/services)
    this.deployRole.addToPolicy(
      new iam.PolicyStatement({
        sid: "EcsServiceManagement",
        effect: iam.Effect.ALLOW,
        actions: ["ecs:DescribeServices", "ecs:UpdateService"],
        resources: [
          `arn:${cdk.Aws.PARTITION}:ecs:*:${cdk.Aws.ACCOUNT_ID}:service/${props.projectName}-*/*`,
        ],
      }),
    );

    // ECS task definition management (cannot be resource-scoped, restricted by family condition)
    this.deployRole.addToPolicy(
      new iam.PolicyStatement({
        sid: "EcsTaskDefinitions",
        effect: iam.Effect.ALLOW,
        actions: ["ecs:DescribeTaskDefinition", "ecs:RegisterTaskDefinition"],
        resources: ["*"],
        conditions: {
          StringLike: {
            "ecs:task-definition-family": `${props.projectName}-*`,
          },
        },
      }),
    );

    // Allow describing any task definition (read-only, needed to fetch current task def)
    this.deployRole.addToPolicy(
      new iam.PolicyStatement({
        sid: "EcsDescribeTaskDefinition",
        effect: iam.Effect.ALLOW,
        actions: ["ecs:DescribeTaskDefinition"],
        resources: ["*"],
      }),
    );

    // IAM PassRole — scoped to project and CDK-generated ECS roles only
    this.deployRole.addToPolicy(
      new iam.PolicyStatement({
        sid: "PassRoleToEcs",
        effect: iam.Effect.ALLOW,
        actions: ["iam:PassRole"],
        resources: [
          `arn:${cdk.Aws.PARTITION}:iam::${cdk.Aws.ACCOUNT_ID}:role/${props.projectName}-*`,
          `arn:${cdk.Aws.PARTITION}:iam::${cdk.Aws.ACCOUNT_ID}:role/StagingAppStack-*`,
          `arn:${cdk.Aws.PARTITION}:iam::${cdk.Aws.ACCOUNT_ID}:role/ProductionAppStack-*`,
        ],
        conditions: {
          StringEquals: {
            "iam:PassedToService": "ecs-tasks.amazonaws.com",
          },
        },
      }),
    );
  }
}
