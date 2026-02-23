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
          "token.actions.githubusercontent.com:sub": `repo:${props.githubOrg}/${props.githubRepo}:ref:refs/heads/main`,
        },
        StringEquals: {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
        },
      }),
    });

    this.deployRole.addToPolicy(
      new iam.PolicyStatement({
        effect: iam.Effect.ALLOW,
        actions: ["sts:AssumeRole"],
        resources: [`arn:aws:iam::${cdk.Aws.ACCOUNT_ID}:role/cdk-*`],
      }),
    );
  }
}
