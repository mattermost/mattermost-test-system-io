import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as acm from "aws-cdk-lib/aws-certificatemanager";
import * as elbv2 from "aws-cdk-lib/aws-elasticloadbalancingv2";
import * as logs from "aws-cdk-lib/aws-logs";
import * as route53 from "aws-cdk-lib/aws-route53";
import * as s3 from "aws-cdk-lib/aws-s3";
import { Construct } from "constructs";

export interface NetworkingProps {
  readonly environment: string;
  readonly projectName: string;
  readonly vpcCidr?: string;
  readonly domainName: string;
  readonly subdomainNames: string[];
  readonly route53ZoneId: string;
  readonly appPort?: number;
}

export class Networking extends Construct {
  public readonly vpc: ec2.Vpc;
  public readonly alb: elbv2.ApplicationLoadBalancer;
  public readonly httpsListener: elbv2.ApplicationListener;
  public readonly certificate: acm.Certificate;
  public readonly appSecurityGroup: ec2.SecurityGroup;
  public readonly hostedZone: route53.IHostedZone;

  constructor(scope: Construct, id: string, props: NetworkingProps) {
    super(scope, id);

    const vpcCidr = props.vpcCidr ?? "10.0.0.0/16";
    const appPort = props.appPort ?? 8080;
    const prefix = `${props.projectName}-${props.environment}`;

    // VPC with 2 AZs, public + private subnets, NAT gateway
    this.vpc = new ec2.Vpc(this, "Vpc", {
      vpcName: `${prefix}-vpc`,
      ipAddresses: ec2.IpAddresses.cidr(vpcCidr),
      maxAzs: 2,
      natGateways: 1,
      subnetConfiguration: [
        {
          name: "Public",
          subnetType: ec2.SubnetType.PUBLIC,
          cidrMask: 24,
        },
        {
          name: "Private",
          subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS,
          cidrMask: 24,
        },
      ],
    });

    // VPC Flow Logs to CloudWatch for network traffic visibility
    this.vpc.addFlowLog("FlowLog", {
      destination: ec2.FlowLogDestination.toCloudWatchLogs(
        new logs.LogGroup(this, "VpcFlowLogGroup", {
          logGroupName: `/vpc/${prefix}/flow-logs`,
          retention: logs.RetentionDays.ONE_MONTH,
          removalPolicy: cdk.RemovalPolicy.DESTROY,
        }),
      ),
      trafficType: ec2.FlowLogTrafficType.ALL,
    });

    // Look up Route 53 hosted zone
    this.hostedZone = route53.HostedZone.fromHostedZoneAttributes(this, "HostedZone", {
      hostedZoneId: props.route53ZoneId,
      zoneName: props.domainName,
    });

    // ACM certificate with DNS validation via Route 53
    const subjectAlternativeNames = props.subdomainNames.map((sub) => `${sub}.${props.domainName}`);
    this.certificate = new acm.Certificate(this, "Certificate", {
      domainName: `*.${props.domainName}`,
      subjectAlternativeNames,
      validation: acm.CertificateValidation.fromDns(this.hostedZone),
    });

    // ALB security group — allow HTTPS/HTTP from anywhere (IPv4 + IPv6)
    const albSecurityGroup = new ec2.SecurityGroup(this, "AlbSecurityGroup", {
      vpc: this.vpc,
      securityGroupName: `${prefix}-alb-sg`,
      description: "Security group for the Application Load Balancer",
      allowAllOutbound: true,
    });
    albSecurityGroup.addIngressRule(
      ec2.Peer.anyIpv4(),
      ec2.Port.tcp(443),
      "Allow HTTPS from anywhere (IPv4)",
    );
    albSecurityGroup.addIngressRule(
      ec2.Peer.anyIpv6(),
      ec2.Port.tcp(443),
      "Allow HTTPS from anywhere (IPv6)",
    );
    albSecurityGroup.addIngressRule(
      ec2.Peer.anyIpv4(),
      ec2.Port.tcp(80),
      "Allow HTTP from anywhere (IPv4, redirects to HTTPS)",
    );
    albSecurityGroup.addIngressRule(
      ec2.Peer.anyIpv6(),
      ec2.Port.tcp(80),
      "Allow HTTP from anywhere (IPv6, redirects to HTTPS)",
    );

    // S3 bucket for ALB access logs
    const albAccessLogBucket = new s3.Bucket(this, "AlbAccessLogBucket", {
      bucketName: `${props.projectName}-alb-access-logs`,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      enforceSSL: true,
      lifecycleRules: [
        {
          expiration: cdk.Duration.days(90),
          abortIncompleteMultipartUploadAfter: cdk.Duration.days(7),
        },
      ],
      removalPolicy: cdk.RemovalPolicy.RETAIN,
    });

    // Application Load Balancer (internet-facing, in public subnets)
    this.alb = new elbv2.ApplicationLoadBalancer(this, "Alb", {
      loadBalancerName: `tsio-${props.environment}-alb`,
      vpc: this.vpc,
      internetFacing: true,
      vpcSubnets: { subnetType: ec2.SubnetType.PUBLIC },
      securityGroup: albSecurityGroup,
      dropInvalidHeaderFields: true,
    });

    this.alb.logAccessLogs(albAccessLogBucket, "alb-logs");

    // HTTPS listener (port 443) with the ACM cert, TLS 1.3 policy
    this.httpsListener = this.alb.addListener("HttpsListener", {
      port: 443,
      protocol: elbv2.ApplicationProtocol.HTTPS,
      certificates: [this.certificate],
      sslPolicy: elbv2.SslPolicy.TLS13_RES,
      defaultAction: elbv2.ListenerAction.fixedResponse(404, {
        contentType: "text/plain",
        messageBody: "Not Found",
      }),
    });

    // HTTP listener (port 80) with redirect to HTTPS
    this.alb.addListener("HttpListener", {
      port: 80,
      protocol: elbv2.ApplicationProtocol.HTTP,
      defaultAction: elbv2.ListenerAction.redirect({
        protocol: "HTTPS",
        port: "443",
        permanent: true,
      }),
    });

    // App security group (allows inbound from ALB on appPort)
    this.appSecurityGroup = new ec2.SecurityGroup(this, "AppSecurityGroup", {
      vpc: this.vpc,
      securityGroupName: `${prefix}-app-sg`,
      description: "Security group for the application service",
      allowAllOutbound: true,
    });
    this.appSecurityGroup.addIngressRule(
      ec2.Peer.securityGroupId(albSecurityGroup.securityGroupId),
      ec2.Port.tcp(appPort),
      `Allow inbound from ALB on port ${appPort}`,
    );
  }
}
