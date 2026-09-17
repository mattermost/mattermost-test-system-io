import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";

/** Tiny argv parser: `--name value`, `--flag`, positionals. */
export function parseArgs(argv: string[]): { opts: Record<string, string>; positional: string[] } {
  const opts: Record<string, string> = {};
  const positional: string[] = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith("--")) {
      positional.push(a);
      continue;
    }
    const name = a.slice(2);
    const next = argv[i + 1];
    if (next === undefined || next.startsWith("--")) opts[name] = "true";
    else {
      opts[name] = next;
      i++;
    }
  }
  return { opts, positional };
}

export function readJSON<T>(path: string, fallback: T): T {
  return existsSync(path) ? (JSON.parse(readFileSync(path, "utf8")) as T) : fallback;
}
export function writeJSON(path: string, value: unknown, pretty = false): void {
  writeFileSync(path, pretty ? JSON.stringify(value, null, 1) : JSON.stringify(value));
}
export function readJSONL<T>(path: string): T[] {
  if (!existsSync(path)) return [];
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((l) => l.trim())
    .map((l) => JSON.parse(l) as T);
}
export const log = (message: string) => process.stderr.write(message + "\n");

/** A JSON file used as a memo for slow lookups (GitHub, model responses). */
export class FileCache {
  private data: Record<string, unknown>;
  constructor(private readonly path: string) {
    this.data = readJSON(path, {});
  }
  keys(): string[] {
    return Object.keys(this.data);
  }
  get<T>(key: string): T | undefined {
    return this.data[key] as T | undefined;
  }
  set(key: string, value: unknown): void {
    this.data[key] = value;
    writeJSON(this.path, this.data);
  }
  async remember<T>(key: string, compute: () => Promise<T>): Promise<T> {
    if (key in this.data) return this.data[key] as T;
    const value = await compute();
    this.set(key, value);
    return value;
  }
}

/** `gh api` with the user's credentials; null when the request fails. */
export function gh<T>(path: string, paginate = false): T | null {
  try {
    const text = execFileSync("gh", ["api", ...(paginate ? ["--paginate"] : []), path], {
      encoding: "utf8",
      maxBuffer: 512 * 1024 * 1024,
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
    if (!paginate || !text.startsWith("[")) return JSON.parse(text) as T;
    // --paginate concatenates one JSON array per page.
    const items: unknown[] = [];
    for (const page of text.split(/\]\s*\[/)) {
      const body = page.replace(/^\[/, "").replace(/\]$/, "");
      if (body.trim()) items.push(...(JSON.parse(`[${body}]`) as unknown[]));
    }
    return items as T;
  } catch {
    return null;
  }
}

let backtestContainer = "";
/** psql inside the docker container publishing :6432 (the backtest database). */
export function psql(sql: string): string {
  return psqlMany([sql])[0];
}
/** Several statements in one psql process; one result string per statement. */
export function psqlMany(sqls: string[]): string[] {
  if (!backtestContainer) {
    backtestContainer = execFileSync("docker", ["ps", "-qf", "publish=6432"], { encoding: "utf8" })
      .trim()
      .split(/\s+/)[0];
    if (!backtestContainer) throw new Error("no docker container publishes port 6432");
  }
  const separator = "__TSIO_SEP__";
  const args = ["exec", backtestContainer, "psql", "-U", "tsio", "-d", "tsio", "-tA"];
  for (const sql of sqls) args.push("-c", sql, "-c", `\\echo ${separator}`);
  const out = execFileSync("docker", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  const parts = out.split(separator + "\n");
  return sqls.map((_, i) => (parts[i] ?? "").trim());
}
/** Dollar-quote a literal for psql. */
export const q = (s: string) => "$q$" + s.replace(/\$/g, "") + "$q$";

export async function tsio<T>(
  base: string,
  path: string,
  init: { method?: string; body?: unknown; headers?: Record<string, string> } = {},
): Promise<{ status: number; body: T }> {
  const response = await fetch(base + path, {
    method: init.method ?? "GET",
    headers: { "Content-Type": "application/json", ...init.headers },
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
    signal: AbortSignal.timeout(300000),
  });
  const text = await response.text();
  let body: T;
  try {
    body = JSON.parse(text) as T;
  } catch {
    body = { error: text.slice(0, 200) } as T;
  }
  return { status: response.status, body };
}

export const GREEN_VERDICTS = new Set(["SUCCESS", "NEUTRAL"]);
export const VERIFIED_LABELS: Record<string, string[]> = {
  "mattermost/mattermost-mobile": ["E2E/Verified"],
  "mattermost/mattermost": ["E2E Tests/verified"],
};
export const OVERRIDE_LABELS = ["E2E/Override"];
export const BASE_REF: Record<string, string> = {
  "mattermost/mattermost-mobile": "main",
  "mattermost/mattermost": "master",
};

/** Required-status context the producer would have used for this report group. */
export function contextFor(repo: string, name: string): string {
  if (repo === "mattermost/mattermost-mobile") {
    for (const prefix of ["mobile-pr-", "mobile-main-"])
      if (name.startsWith(prefix)) return "e2e-test/" + name.slice(prefix.length);
    return "e2e-test/" + name;
  }
  const lane = name.slice(name.lastIndexOf("-") + 1);
  return "e2e-test/" + name.slice(0, -lane.length - 1) + "/" + lane;
}

/** Mirror of identity.InferLane in the server. */
export function laneFor(name: string): string {
  for (const marker of ["-release-cut", "-release", "-master", "-main"])
    if (name.endsWith(marker)) name = name.slice(0, -marker.length);
  for (const token of [
    "detox-ios",
    "detox-android",
    "detox-ipad",
    "maestro-ios",
    "maestro-android",
    "enterprise",
    "fips",
  ]) {
    const i = name.indexOf("-" + token);
    if (i < 0) continue;
    let rest = name.slice(i + 1 + token.length);
    if (rest && !rest.startsWith("-")) continue;
    if (rest.endsWith("-e2e")) rest = rest.slice(0, -4);
    return token.replace("detox-", "").replace("maestro-", "") + rest;
  }
  return "";
}

/** One PR run as recorded by the backtest (JSON next to the markdown report). */
export interface BacktestFinding {
  class: string;
  title: string;
  file: string;
  reason: string;
  trunk: {
    runs: number | null;
    fails: number | null;
    flaky: number | null;
    consecutive_fails: number | null;
  };
  excerpt: string;
  match: boolean | null;
}
export interface BacktestRow {
  repo: string;
  pr: number;
  name: string;
  sha: string;
  run_at: string;
  state: string | null;
  merged: boolean;
  failed: number;
  raw_failed: number;
  flaky: number;
  truth: string;
  verdict: string;
  confidence: number | null;
  classes: Record<string, number>;
  counts: Record<string, number>;
  agree: boolean | null;
  error: string | null;
  as_of: string;
  base_sha: string;
  changed: number;
  waived_at: string | null;
  findings: BacktestFinding[];
  recurring_tests?: number;
  failing_tests?: number;
}
