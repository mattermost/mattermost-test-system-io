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

export interface RunContextInputs {
  stage?: string;
  eventName?: string;
  baseRef?: string;
  headRef?: string;
  platform?: string;
  serverVersion?: string;
  appVersion?: string;
  edition?: string;
}

function nonEmpty(value: string | undefined): string | undefined {
  const trimmed = typeof value === "string" ? value.trim() : undefined;
  return trimmed ? trimmed : undefined;
}

/**
 * Resolves optional run context with precedence: explicit input > composite-identity > GITHUB_* env.
 * Empty values are omitted so unset fields never travel as empty strings on the wire.
 */
export function resolveRunContext(
  inputs: RunContextInputs,
  identity: RunContext,
  env: Record<string, string | undefined> = process.env,
): RunContext {
  const out: RunContext = {};
  const pairs: [keyof RunContext, string | undefined][] = [
    ["stage", nonEmpty(inputs.stage) ?? nonEmpty(identity.stage)],
    [
      "event_name",
      nonEmpty(inputs.eventName) ??
        nonEmpty(identity.event_name) ??
        nonEmpty(env.GITHUB_EVENT_NAME),
    ],
    [
      "base_ref",
      nonEmpty(inputs.baseRef) ?? nonEmpty(identity.base_ref) ?? nonEmpty(env.GITHUB_BASE_REF),
    ],
    [
      "head_ref",
      nonEmpty(inputs.headRef) ?? nonEmpty(identity.head_ref) ?? nonEmpty(env.GITHUB_HEAD_REF),
    ],
    ["platform", nonEmpty(inputs.platform) ?? nonEmpty(identity.platform)],
    ["server_version", nonEmpty(inputs.serverVersion) ?? nonEmpty(identity.server_version)],
    ["app_version", nonEmpty(inputs.appVersion) ?? nonEmpty(identity.app_version)],
    ["edition", nonEmpty(inputs.edition) ?? nonEmpty(identity.edition)],
  ];
  for (const [key, val] of pairs) {
    if (val !== undefined) out[key] = val;
  }
  return out;
}
