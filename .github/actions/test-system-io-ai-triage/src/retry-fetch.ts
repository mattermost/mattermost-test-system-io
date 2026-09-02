/**
 * retryFetch wraps a `fetch` call with bounded retries on transient
 * failures (HTTP 408, 429, 5xx) and network-level errors. Permanent 4xx
 * responses (401/403/404 etc.) return immediately so callers can act on
 * them without sleeping through a useless retry budget.
 *
 * Behaviour:
 *   - HTTP 2xx → return Response.
 *   - HTTP 408/429/5xx → retry with backoff, return last Response (or
 *     throw the last network error) once the budget is spent.
 *   - HTTP other 4xx → return Response immediately (caller decides).
 *   - Network/abort/DNS error → retry with backoff, throw last error
 *     once budget is spent.
 *
 * `label` is prepended to the warning log so consumers can tell which
 * endpoint flaked in the action's output.
 */

import * as core from "@actions/core";

const DEFAULT_DELAYS_MS = [400, 1200, 3000];

export async function retryFetch(
  input: string,
  init: RequestInit,
  label: string,
): Promise<Response> {
  const delays = DEFAULT_DELAYS_MS;
  let lastErr: unknown;
  for (let attempt = 0; attempt <= delays.length; attempt++) {
    try {
      const res = await fetch(input, init);
      if (res.ok || !isRetryableStatus(res.status)) return res;
      lastErr = new Error(`HTTP ${res.status} ${await safeText(res)}`);
    } catch (err) {
      lastErr = err;
    }
    if (attempt === delays.length) break;
    const ms = delays[attempt]! + Math.floor(Math.random() * 200);
    core.warning(
      `${label}: fetch failed (attempt ${attempt + 1}/${delays.length + 1}): ` +
        `${(lastErr as Error).message}; retrying in ${ms}ms`,
    );
    await new Promise<void>((r) => setTimeout(r, ms));
  }
  throw lastErr;
}

export function isRetryableStatus(status: number): boolean {
  return status === 408 || status === 429 || (status >= 500 && status < 600);
}

export async function safeText(res: Response, max = 500): Promise<string> {
  try {
    const t = await res.text();
    return t.length <= max ? t : `${t.slice(0, max)}…(${t.length - max} more chars)`;
  } catch {
    return "<unreadable body>";
  }
}

/**
 * Parse a JSON response, or fail with an error a human can act on.
 *
 * WHY THIS EXISTS. A TSIO deployment that does not have the /api/v1/triage/*
 * endpoints does NOT return 404 for them — the web app is served from the same
 * origin, so unknown paths fall through to the SPA and come back as **HTTP 200
 * with an HTML document**. Every `res.ok` check passes, and the failure only
 * surfaces as `SyntaxError: Unexpected token '<'` from JSON.parse, which
 * crashes the action with a stack trace and no indication of the real cause.
 *
 * That is a fail-closed violation in spirit: the check does end up red, but
 * with a crashed job instead of a reason anyone can read. Observed for real on
 * mattermost#38154 when the triage job was pointed at a TSIO without these
 * endpoints deployed.
 *
 * It parses FIRST and only diagnoses on failure, rather than gating on
 * content-type. Gating on the header rejects valid JSON served as text/plain —
 * which real proxies and hand-rolled test doubles both do — and a check that
 * refuses good data to catch bad data is a worse trade than parsing and
 * explaining.
 */
export async function parseJSON<T>(res: Response, label: string): Promise<T> {
  const text = await res.text();
  try {
    return JSON.parse(text) as T;
  } catch {
    const head = text.slice(0, 200).replace(/\s+/g, " ").trim();
    const looksLikeHTML = /^\s*(<!doctype|<html)/i.test(text);
    const diagnosis = looksLikeHTML
      ? "The response is an HTML page, which means this TSIO deployment does not have " +
        "the /api/v1/triage endpoints (unknown paths fall through to the web app). " +
        "Check the use-staging input."
      : "The response is neither JSON nor HTML.";
    throw new Error(
      `${label} did not return JSON (HTTP ${res.status}). ${diagnosis} Body starts: ${head}`,
    );
  }
}
