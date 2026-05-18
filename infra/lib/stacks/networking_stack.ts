import * as cdk from "aws-cdk-lib";
import * as acm from "aws-cdk-lib/aws-certificatemanager";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as elbv2 from "aws-cdk-lib/aws-elasticloadbalancingv2";
import * as route53 from "aws-cdk-lib/aws-route53";
import { Construct } from "constructs";

import { AppConfig } from "../config";
import { Networking } from "../constructs/networking";

export interface NetworkingStackProps extends cdk.StackProps {
  readonly config: AppConfig;
}

export class NetworkingStack extends cdk.Stack {
  public readonly vpc: ec2.Vpc;
  public readonly alb: elbv2.ApplicationLoadBalancer;
  public readonly httpsListener: elbv2.ApplicationListener;
  public readonly certificate: acm.ICertificate;
  public readonly appSecurityGroup: ec2.SecurityGroup;
  public readonly hostedZone: route53.IHostedZone;

  constructor(scope: Construct, id: string, props: NetworkingStackProps) {
    super(scope, id, props);

    const { config } = props;

    if (!config.route53ZoneId) {
      throw new Error(
        "ROUTE53_ZONE_ID env var is required (Route 53 hosted zone — see infra/lib/config.ts)",
      );
    }
    if (!config.certificateArn) {
      throw new Error(
        "CERTIFICATE_ARN env var is required (pinned ACM cert ARN — see infra/lib/config.ts)",
      );
    }

    const networking = new Networking(this, "Networking", {
      environment: "shared",
      projectName: config.projectName,
      domainName: config.domainName,
      route53ZoneId: config.route53ZoneId,
      certificateArn: config.certificateArn,
    });

    this.vpc = networking.vpc;
    this.alb = networking.alb;
    this.httpsListener = networking.httpsListener;
    this.certificate = networking.certificate;
    this.appSecurityGroup = networking.appSecurityGroup;
    this.hostedZone = networking.hostedZone;
  }
}
