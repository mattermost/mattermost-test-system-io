/**
 * W14 queue — promotions first (release guard, SLA breach, override filed
 * them; they own the head), then the organic flakiness ranking, capped at
 * the working depth.
 */
import type { QueueEntry, QueueResponse } from "./types.ts";

export interface QueueFetchFn {
  (baseURL: string, repo: string, depth: number): Promise<QueueResponse>;
}


/** Fetch and flatten the queue into pick order. */
export async function pickQueue(
  fetch: QueueFetchFn,
  baseURL: string,
  repo: string,
  depth: number,
): Promise<QueueEntry[]> {
  const res = await fetch(baseURL, repo, depth);
  return [...(res.promoted ?? []), ...(res.ranked ?? [])];
}

/** Take up to `n` items, respecting the working depth. */
export function take(items: QueueEntry[], n: number): QueueEntry[] {
  return items.slice(0, Math.max(0, n));
}

/**
 * The queue URL — AUTHENTICATED read (B7: it returns row-level data). The
 * caller must send the OIDC bearer; see main.ts fetchQueue.
 */
export function queueURL(baseURL: string, repo: string): string {
  const params = new URLSearchParams({ repo, window: "30d" });
  return `${baseURL}/api/v1/triage/stabilization/queue?${params.toString()}`;
}

/**
 * The result of a queue fetch. `ok:false` means the loop must stand down with
 * a distinct `queue_unavailable` action (never the same "queue empty" a
 * healthy-but-empty queue emits) — a misconfigured OIDC audience must not
 * masquerade as "nothing to do" forever.
 */
export interface QueueFetchResult {
  ok: boolean;
  /** HTTP status; 0 for a network/transport failure. */
  status: number;
  promoted: QueueEntry[];
  ranked: QueueEntry[];
}

/**
 * Fetch the queue with the OIDC bearer and classify the outcome. 401/403 is a
 * PERMANENT auth misconfiguration — it fails the job (setFailed), not a
 * transient that fail-softs. 5xx/network fail-soft with a warning and a
 * distinct status so the outputs differ from a genuinely empty queue.
 */
export async function fetchQueue(opts: {
  baseURL: string;
  repo: string;
  audience: string;
  getIDToken: (audience: string) => Promise<string>;
  fetch: typeof fetch;
  setFailed: (msg: string) => void;
  warning: (msg: string) => void;
}): Promise<QueueFetchResult> {
  const url = queueURL(opts.baseURL, opts.repo);
  try {
    const bearer = await opts.getIDToken(opts.audience);
    const res = await opts.fetch(url, { headers: { Authorization: `Bearer ${bearer}` } });
    if (res.status === 401 || res.status === 403) {
      opts.setFailed(
        `queue fetch ${res.status} — OIDC audience/authorization misconfigured (permanent, not transient)`,
      );
      return { ok: false, status: res.status, promoted: [], ranked: [] };
    }
    if (!res.ok) {
      opts.warning(`queue fetch failed (${res.status}) — standing down this run`);
      return { ok: false, status: res.status, promoted: [], ranked: [] };
    }
    const body = (await res.json()) as { promoted: QueueEntry[]; ranked: QueueEntry[] };
    return { ok: true, status: res.status, promoted: body.promoted ?? [], ranked: body.ranked ?? [] };
  } catch (err) {
    opts.warning(`queue fetch failed (${(err as Error).message}) — standing down this run`);
    return { ok: false, status: 0, promoted: [], ranked: [] };
  }
}
