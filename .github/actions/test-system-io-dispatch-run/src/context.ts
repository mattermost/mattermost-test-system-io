export interface RunContext {
  stage?: string;
  event_name?: string;
  base_ref?: string;
  head_ref?: string;
  platform?: string;
  server_version?: string;
  app_version?: string;
  edition?: string;
}

export interface WorkerContextInputs {
  platform?: string;
  framework: string;
  maestroPlatform?: string;
  detoxConfig?: string;
}

const RUN_CONTEXT_KEYS: readonly (keyof RunContext)[] = [
  "stage",
  "event_name",
  "base_ref",
  "head_ref",
  "platform",
  "server_version",
  "app_version",
  "edition",
];

function nonEmpty(value: string | undefined): string | undefined {
  const trimmed = typeof value === "string" ? value.trim() : undefined;
  return trimmed ? trimmed : undefined;
}

/**
 * Strips empty, whitespace-only, nullish, or non-string RunContext keys from a parsed identity
 * payload in place so spreading it into request bodies never leaks blank or invalid values.
 */
export function stripBlankRunContext(identity: RunContext): void {
  for (const key of RUN_CONTEXT_KEYS) {
    const val = identity[key];
    if (typeof val !== "string") {
      delete identity[key];
      continue;
    }
    const trimmed = val.trim();
    if (!trimmed) {
      delete identity[key];
    } else {
      identity[key] = trimmed;
    }
  }
}

function detoxPlatformFromConfig(detoxConfig: string | undefined): string | undefined {
  const lower = detoxConfig?.trim().toLowerCase();
  if (!lower) return undefined;
  if (lower === "ios" || lower.startsWith("ios.")) return "ios";
  if (lower === "android" || lower.startsWith("android.")) return "android";
  return undefined;
}

/** Resolves worker platform: explicit input > composite-identity > maestro-platform > detox-config prefix. */
export function resolveWorkerPlatform(
  inputs: WorkerContextInputs,
  identity: RunContext,
): string | undefined {
  const explicit = nonEmpty(inputs.platform) ?? nonEmpty(identity.platform);
  if (explicit !== undefined) return explicit;
  if (inputs.framework === "maestro") {
    return nonEmpty(inputs.maestroPlatform);
  }
  if (inputs.framework === "detox") {
    return detoxPlatformFromConfig(inputs.detoxConfig);
  }
  return undefined;
}

/**
 * Resolves optional run context forwarded on /reports/register, omitting empty strings.
 */
export function resolveRegisterContext(
  inputs: WorkerContextInputs,
  identity: RunContext,
  env: Record<string, string | undefined> = process.env,
): RunContext {
  const out: RunContext = {};
  const pairs: [keyof RunContext, string | undefined][] = [
    ["stage", nonEmpty(identity.stage)],
    ["event_name", nonEmpty(identity.event_name) ?? nonEmpty(env.GITHUB_EVENT_NAME)],
    ["base_ref", nonEmpty(identity.base_ref) ?? nonEmpty(env.GITHUB_BASE_REF)],
    ["head_ref", nonEmpty(identity.head_ref) ?? nonEmpty(env.GITHUB_HEAD_REF)],
    ["platform", resolveWorkerPlatform(inputs, identity)],
    ["server_version", nonEmpty(identity.server_version)],
    ["app_version", nonEmpty(identity.app_version)],
    ["edition", nonEmpty(identity.edition)],
  ];
  for (const [key, val] of pairs) {
    if (val !== undefined) out[key] = val;
  }
  return out;
}
