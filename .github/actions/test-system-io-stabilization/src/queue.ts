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

/** The queue URL — public read, same as the other triage reads. */
export function queueURL(baseURL: string, repo: string): string {
  const params = new URLSearchParams({ repo, window: "30d" });
  return `${baseURL}/api/v1/triage/stabilization/queue?${params.toString()}`;
}
