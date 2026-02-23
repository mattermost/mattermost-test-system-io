import * as ec2 from "aws-cdk-lib/aws-ec2";

export interface StagingEnvConfig {
  readonly subdomain: string;
  readonly cpu: number;
  readonly memoryLimitMiB: number;
  readonly desiredCount: number;
  readonly minimumHealthyPercent: number;
  readonly maximumPercent: number;
  readonly postgresVersion: string;
  readonly postgresEphemeralStorageGiB: number;
}

export interface ProductionEnvConfig {
  readonly subdomain: string;
  readonly cpu: number;
  readonly memoryLimitMiB: number;
  readonly desiredCount: number;
  readonly minimumHealthyPercent: number;
  readonly maximumPercent: number;
  readonly dbInstanceClass: ec2.InstanceType;
  readonly dbAllocatedStorage: number;
  readonly dbBackupRetentionDays: number;
}

export interface AppConfig {
  readonly projectName: string;
  readonly domainName: string;
  readonly route53ZoneId: string;
  readonly allSubdomains: string[];
  readonly staging: StagingEnvConfig;
  readonly production: ProductionEnvConfig;
}

export const APP_CONFIG: AppConfig = {
  projectName: "mattermost-test-system-io",
  domainName: "test.mattermost.com",
  route53ZoneId: process.env.ROUTE53_ZONE_ID ?? "",
  allSubdomains: ["staging-test-io", "test-io"],

  staging: {
    subdomain: "staging-test-io",
    cpu: 512,
    memoryLimitMiB: 1024,
    desiredCount: 1,
    minimumHealthyPercent: 0,
    maximumPercent: 200,
    postgresVersion: "18.1",
    postgresEphemeralStorageGiB: 30,
  },

  production: {
    subdomain: "test-io",
    cpu: 1024,
    memoryLimitMiB: 2048,
    desiredCount: 2,
    minimumHealthyPercent: 100,
    maximumPercent: 200,
    dbInstanceClass: ec2.InstanceType.of(ec2.InstanceClass.T4G, ec2.InstanceSize.MICRO),
    dbAllocatedStorage: 100,
    dbBackupRetentionDays: 7,
  },
};

export const SHARED_CONFIG = {
  projectName: "mattermost-test-system-io",
  githubOrg: "mattermost",
  githubRepo: "mattermost-test-system-io",
};
