/**
 * retryFetch wraps a `fetch` call with bounded retries on transient
 * failures (HTTP 408, 429, 5xx) and network-level errors. Permanent 4xx
 * responses (401/403/404 etc.) return immediately so callers can act on
 * them without sleeping through a useless retry budget.
 *
 * Behaviour:
 *   - HTTP 2xx → return Response.
 *   - HTTP 408/429/5xx → retry with backoff; once the budget is spent,
 *     throw an Error wrapping the final status + body (never a Response).
 *   - HTTP other 4xx → return Response immediately (caller decides).
 *   - Network/abort/DNS error → retry with backoff, throw the last
 *     error once budget is spent.
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
    // Normalize the thrown value — a non-Error throw (string, plain object,
    // undici-internal symbol) would otherwise stringify to "undefined".
    const errMsg = lastErr instanceof Error ? lastErr.message : String(lastErr);
    core.warning(
      `${label}: fetch failed (attempt ${attempt + 1}/${delays.length + 1}): ` +
        `${errMsg}; retrying in ${ms}ms`,
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
