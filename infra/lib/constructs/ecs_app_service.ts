import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as ecs from "aws-cdk-lib/aws-ecs";
import * as elbv2 from "aws-cdk-lib/aws-elasticloadbalancingv2";
import * as route53 from "aws-cdk-lib/aws-route53";
import * as route53targets from "aws-cdk-lib/aws-route53-targets";
import { Construct } from "constructs";

export interface DbReadinessCheck {
  readonly host: string;
  readonly port?: number;
  readonly user?: string;
  readonly dbName?: string;
  readonly postgresImage?: string;
}

export interface EcsAppServiceProps {
  readonly environment: string;
  readonly projectName: string;
  readonly serviceName: string;
  readonly cluster: ecs.ICluster;
  readonly imageTag: string;
  readonly imageRepo?: string;
  readonly desiredCount?: number;
  readonly cpu?: number;
  readonly memoryLimitMiB?: number;
  readonly httpsListener: elbv2.IApplicationListener;
  readonly alb: elbv2.IApplicationLoadBalancer;
  readonly vpc: ec2.IVpc;
  readonly appSecurityGroup: ec2.ISecurityGroup;
  readonly domainName: string;
  readonly hostedZone: route53.IHostedZone;
  readonly listenerPriority: number;
  readonly environmentVariables?: Record<string, string>;
  readonly secrets?: Record<string, ecs.Secret>;
  readonly minimumHealthyPercent?: number;
  readonly maximumPercent?: number;
  readonly healthCheckGracePeriod?: cdk.Duration;
  readonly dbReadinessCheck?: DbReadinessCheck;
}

export class EcsAppService extends Construct {
  public readonly service: ecs.FargateService;
  public readonly taskDefinition: ecs.FargateTaskDefinition;

  constructor(scope: Construct, id: string, props: EcsAppServiceProps) {
    super(scope, id);

    const imageRepo = props.imageRepo ?? "mattermostdevelopment/mattermost-test-system-io";
    const cpu = props.cpu ?? 512;
    const memoryLimitMiB = props.memoryLimitMiB ?? 1024;
    const desiredCount = props.desiredCount ?? 1;
    const minimumHealthyPercent = props.minimumHealthyPercent ?? 100;
    const maximumPercent = props.maximumPercent ?? 200;
    const healthCheckGracePeriod = props.healthCheckGracePeriod ?? cdk.Duration.seconds(60);
    const fullDomainName = `${props.serviceName}.${props.domainName}`;
    const prefix = `${props.projectName}-${props.environment}`;

    // Task definition
    this.taskDefinition = new ecs.FargateTaskDefinition(this, "TaskDefinition", {
      family: `${prefix}-${props.serviceName}`,
      cpu,
      memoryLimitMiB,
    });

    const appContainer = this.taskDefinition.addContainer("app", {
      containerName: "app",
      image: ecs.ContainerImage.fromRegistry(`${imageRepo}:${props.imageTag}`),
      portMappings: [{ containerPort: 8080, protocol: ecs.Protocol.TCP }],
      environment: props.environmentVariables ?? {},
      secrets: props.secrets ?? {},
      logging: ecs.LogDrivers.awsLogs({
        streamPrefix: `${prefix}-${props.serviceName}`,
      }),
      essential: true,
    });

    // Init container: wait for Postgres to be ready before starting the app
    if (props.dbReadinessCheck) {
      const db = props.dbReadinessCheck;
      const dbHost = db.host;
      const dbPort = db.port ?? 5432;
      const dbUser = db.user ?? "tsio";
      const dbName = db.dbName ?? "tsio";
      const pgImage = db.postgresImage ?? "postgres:18.1";

      const waitForDb = this.taskDefinition.addContainer("wait-for-db", {
        containerName: "wait-for-db",
        image: ecs.ContainerImage.fromRegistry(pgImage),
        essential: false,
        command: [
          "sh",
          "-c",
          `for i in $(seq 1 30); do pg_isready -h ${dbHost} -p ${dbPort} -U ${dbUser} -d ${dbName} && echo "PostgreSQL is ready" && exit 0; echo "Waiting for PostgreSQL ($i/30)..."; sleep 2; done; echo "PostgreSQL not ready after 60s"; exit 1`,
        ],
        logging: ecs.LogDrivers.awsLogs({
          streamPrefix: `${prefix}-wait-for-db`,
        }),
      });

      appContainer.addContainerDependencies({
        container: waitForDb,
        condition: ecs.ContainerDependencyCondition.SUCCESS,
      });
    }

    // Fargate service
    this.service = new ecs.FargateService(this, "Service", {
      serviceName: `${prefix}-${props.serviceName}`,
      cluster: props.cluster,
      taskDefinition: this.taskDefinition,
      desiredCount,
      minHealthyPercent: minimumHealthyPercent,
      maxHealthyPercent: maximumPercent,
      assignPublicIp: false,
      securityGroups: [props.appSecurityGroup],
      vpcSubnets: { subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS },
      healthCheckGracePeriod,
      circuitBreaker: { rollback: true },
    });

    // Target group
    const targetGroup = new elbv2.ApplicationTargetGroup(this, "TargetGroup", {
      targetGroupName: `tsio-${props.serviceName}`,
      vpc: props.vpc,
      port: 8080,
      protocol: elbv2.ApplicationProtocol.HTTP,
      targets: [this.service],
      healthCheck: {
        path: "/api/v1/health",
        interval: cdk.Duration.seconds(30),
        timeout: cdk.Duration.seconds(5),
        healthyThresholdCount: 2,
        unhealthyThresholdCount: 3,
      },
      deregistrationDelay: cdk.Duration.seconds(30),
    });

    // Listener rule: route by host header to this service
    new elbv2.ApplicationListenerRule(this, "ListenerRule", {
      listener: props.httpsListener,
      priority: props.listenerPriority,
      conditions: [elbv2.ListenerCondition.hostHeaders([fullDomainName])],
      targetGroups: [targetGroup],
    });

    // Route 53 alias record pointing subdomain to the shared ALB
    new route53.ARecord(this, "DnsRecord", {
      zone: props.hostedZone,
      recordName: props.serviceName,
      target: route53.RecordTarget.fromAlias(new route53targets.LoadBalancerTarget(props.alb)),
    });
  }
}
