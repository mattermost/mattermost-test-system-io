#!/usr/bin/env node
import "source-map-support/register";
import { config as loadEnv } from "dotenv";
import * as cdk from "aws-cdk-lib";

loadEnv();

import { SHARED_CONFIG, APP_CONFIG } from "../lib/config";
import { SharedStack } from "../lib/stacks/shared_stack";
import { NetworkingStack } from "../lib/stacks/networking_stack";
import { ProductionDataStack } from "../lib/stacks/production_data_stack";
import { StagingAppStack } from "../lib/stacks/staging_app_stack";
import { ProductionAppStack } from "../lib/stacks/production_app_stack";

const app = new cdk.App();

const env: cdk.Environment = {
  account: process.env.CDK_DEFAULT_ACCOUNT,
  region: process.env.CDK_DEFAULT_REGION,
};

// Stack 1: GitHub OIDC provider + deploy role
new SharedStack(app, "SharedStack", {
  env,
  config: SHARED_CONFIG,
});

// Stack 2: VPC, ALB, ACM certificate, security groups
const networkingStack = new NetworkingStack(app, "NetworkingStack", {
  env,
  config: APP_CONFIG,
});

// Stack 3: Production RDS + S3 (stateful, survives app stack failures)
const productionDataStack = new ProductionDataStack(app, "ProductionDataStack", {
  env,
  projectName: APP_CONFIG.projectName,
  vpc: networkingStack.vpc,
  appSecurityGroup: networkingStack.appSecurityGroup,
  production: APP_CONFIG.production,
});

// Stack 4: Staging ECS cluster + postgres + app service
new StagingAppStack(app, "StagingAppStack", {
  env,
  config: APP_CONFIG,
  vpc: networkingStack.vpc,
  alb: networkingStack.alb,
  httpsListener: networkingStack.httpsListener,
  appSecurityGroup: networkingStack.appSecurityGroup,
  hostedZone: networkingStack.hostedZone,
});

// Stack 5: Production ECS cluster + app service (uses data from Stack 3)
new ProductionAppStack(app, "ProductionAppStack", {
  env,
  config: APP_CONFIG,
  vpc: networkingStack.vpc,
  alb: networkingStack.alb,
  httpsListener: networkingStack.httpsListener,
  appSecurityGroup: networkingStack.appSecurityGroup,
  hostedZone: networkingStack.hostedZone,
  rdsInstance: productionDataStack.rdsInstance,
  rdsSecret: productionDataStack.rdsSecret,
  rdsEndpoint: productionDataStack.rdsEndpoint,
  rdsDbName: productionDataStack.rdsDbName,
  rdsDbUsername: productionDataStack.rdsDbUsername,
  bucket: productionDataStack.bucket,
});
