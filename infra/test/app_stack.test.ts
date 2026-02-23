/// <reference types="vitest" />
import * as cdk from "aws-cdk-lib";
import { Template } from "aws-cdk-lib/assertions";
import { NetworkingStack } from "../lib/stacks/networking_stack";
import { ProductionDataStack } from "../lib/stacks/production_data_stack";
import { StagingAppStack } from "../lib/stacks/staging_app_stack";
import { ProductionAppStack } from "../lib/stacks/production_app_stack";
import { APP_CONFIG, AppConfig } from "../lib/config";

const testConfig: AppConfig = {
  ...APP_CONFIG,
  route53ZoneId: "Z1234567890",
};

const testEnv = { account: "123456789012", region: "us-east-1" };

function createTemplates() {
  const app = new cdk.App();

  const networkingStack = new NetworkingStack(app, "TestNetworkingStack", {
    config: testConfig,
    env: testEnv,
  });

  const productionDataStack = new ProductionDataStack(app, "TestProductionDataStack", {
    env: testEnv,
    projectName: testConfig.projectName,
    vpc: networkingStack.vpc,
    appSecurityGroup: networkingStack.appSecurityGroup,
    production: testConfig.production,
  });

  const stagingAppStack = new StagingAppStack(app, "TestStagingAppStack", {
    config: testConfig,
    env: testEnv,
    vpc: networkingStack.vpc,
    alb: networkingStack.alb,
    httpsListener: networkingStack.httpsListener,
    appSecurityGroup: networkingStack.appSecurityGroup,
    hostedZone: networkingStack.hostedZone,
  });

  const productionAppStack = new ProductionAppStack(app, "TestProductionAppStack", {
    config: testConfig,
    env: testEnv,
    vpc: networkingStack.vpc,
    alb: networkingStack.alb,
    httpsListener: networkingStack.httpsListener,
    appSecurityGroup: networkingStack.appSecurityGroup,
    hostedZone: networkingStack.hostedZone,
    rdsInstance: productionDataStack.rdsInstance,
    rdsSecret: productionDataStack.rdsSecret,
    databaseUrl: productionDataStack.databaseUrl,
    bucket: productionDataStack.bucket,
  });

  return {
    networking: Template.fromStack(networkingStack),
    productionData: Template.fromStack(productionDataStack),
    stagingApp: Template.fromStack(stagingAppStack),
    productionApp: Template.fromStack(productionAppStack),
  };
}

describe("Infrastructure", () => {
  let templates: ReturnType<typeof createTemplates>;

  beforeAll(() => {
    templates = createTemplates();
  });

  describe("NetworkingStack", () => {
    test("creates 1 VPC", () => {
      templates.networking.resourceCountIs("AWS::EC2::VPC", 1);
    });

    test("creates 1 ALB", () => {
      templates.networking.resourceCountIs("AWS::ElasticLoadBalancingV2::LoadBalancer", 1);
    });

    test("creates ACM certificate with DNS validation", () => {
      templates.networking.hasResourceProperties("AWS::CertificateManager::Certificate", {
        ValidationMethod: "DNS",
      });
    });
  });

  describe("ProductionDataStack", () => {
    test("creates RDS PostgreSQL instance", () => {
      templates.productionData.hasResourceProperties("AWS::RDS::DBInstance", {
        Engine: "postgres",
      });
    });

    test("RDS is NOT publicly accessible (FR-030)", () => {
      templates.productionData.hasResourceProperties("AWS::RDS::DBInstance", {
        PubliclyAccessible: false,
      });
    });

    test("creates S3 bucket with public access blocked (FR-030)", () => {
      templates.productionData.hasResourceProperties("AWS::S3::Bucket", {
        PublicAccessBlockConfiguration: {
          BlockPublicAcls: true,
          BlockPublicPolicy: true,
          IgnorePublicAcls: true,
          RestrictPublicBuckets: true,
        },
      });
    });
  });

  describe("StagingAppStack", () => {
    test("creates 1 ECS cluster", () => {
      templates.stagingApp.resourceCountIs("AWS::ECS::Cluster", 1);
    });

    test("creates 2 ECS services (app + postgres)", () => {
      templates.stagingApp.resourceCountIs("AWS::ECS::Service", 2);
    });

    test("creates 1 ALB listener rule", () => {
      templates.stagingApp.resourceCountIs("AWS::ElasticLoadBalancingV2::ListenerRule", 1);
    });

    test("creates S3 bucket with public access blocked (FR-030)", () => {
      templates.stagingApp.hasResourceProperties("AWS::S3::Bucket", {
        PublicAccessBlockConfiguration: {
          BlockPublicAcls: true,
          BlockPublicPolicy: true,
          IgnorePublicAcls: true,
          RestrictPublicBuckets: true,
        },
      });
    });

    test("creates Cloud Map namespace", () => {
      templates.stagingApp.hasResourceProperties("AWS::ServiceDiscovery::PrivateDnsNamespace", {
        Name: "mattermost-test-io.internal",
      });
    });
  });

  describe("ProductionAppStack", () => {
    test("creates 1 ECS cluster", () => {
      templates.productionApp.resourceCountIs("AWS::ECS::Cluster", 1);
    });

    test("creates 1 ECS service (app only)", () => {
      templates.productionApp.resourceCountIs("AWS::ECS::Service", 1);
    });

    test("creates 1 ALB listener rule", () => {
      templates.productionApp.resourceCountIs("AWS::ElasticLoadBalancingV2::ListenerRule", 1);
    });
  });
});
