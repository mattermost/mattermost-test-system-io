import * as cdk from "aws-cdk-lib";
import { Construct } from "constructs";

import { SHARED_CONFIG } from "../config";
import { GithubOidc } from "../constructs/github_oidc";

export interface SharedStackProps extends cdk.StackProps {
  readonly config: typeof SHARED_CONFIG;
}

export class SharedStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: SharedStackProps) {
    super(scope, id, props);

    const { config } = props;

    const oidc = new GithubOidc(this, "GithubOidc", {
      projectName: config.projectName,
      githubOrg: config.githubOrg,
      githubRepo: config.githubRepo,
    });

    new cdk.CfnOutput(this, "DeployRoleArn", {
      value: oidc.deployRole.roleArn,
      description: "ARN of the GitHub Actions deploy role",
      exportName: `${config.projectName}-deploy-role-arn`,
    });
  }
}
