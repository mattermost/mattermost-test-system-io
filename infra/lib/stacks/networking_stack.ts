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
  public readonly certificate: acm.Certificate;
  public readonly appSecurityGroup: ec2.SecurityGroup;
  public readonly hostedZone: route53.IHostedZone;

  constructor(scope: Construct, id: string, props: NetworkingStackProps) {
    super(scope, id, props);

    const { config } = props;

    const networking = new Networking(this, "Networking", {
      environment: "shared",
      projectName: config.projectName,
      domainName: config.domainName,
      subdomainNames: config.allSubdomains,
      route53ZoneId: config.route53ZoneId,
    });

    this.vpc = networking.vpc;
    this.alb = networking.alb;
    this.httpsListener = networking.httpsListener;
    this.certificate = networking.certificate;
    this.appSecurityGroup = networking.appSecurityGroup;
    this.hostedZone = networking.hostedZone;
  }
}
