/**
 * OIDC token caching + retrying fetch wrappers for long-lived workers.
 *
 * GH Actions OIDC tokens have a short TTL (~10 min). A worker that
 * drains a queue for an hour cannot mint once and reuse — the bearer
 * expires mid-loop and the next /complete returns 401. We mint on
 * demand, cache for a few minutes, and on a 401 we invalidate and
 * retry once. Pure transient-network failures, request timeouts, and
 * transient HTTP statuses (notably 504 gateway timeouts on the
 * end-of-worker shard upload) get exponential backoff on top.
 */

import * as core from "@actions/core";

const TOKEN_REFRESH_AGE_MS = 5 * 60 * 1000;

/** HTTP statuses that are safe to retry (gateway blips / rate limits). */
const TRANSIENT_HTTP_STATUS = new Set([408, 429, 502, 503, 504]);

/** Request timeout for JSON control calls (checkout / complete / register). */
export const JSON_REQUEST_TIMEOUT_MS = 30_000;

/** Request timeout for multipart shard uploads — generous since large
 *  screenshot batches stream over a slow runner uplink. */
export const UPLOAD_REQUEST_TIMEOUT_MS = 120_000;

/** Upper bound applied to a server-provided Retry-After. An upstream 429/503
 *  can name an arbitrarily distant delay/date; without a cap that turns a
 *  retry into an unbounded action stall. */
const MAX_RETRY_AFTER_MS = 30_000;

/** Reports whether a status is one the fetch wrappers retry automatically, so
 *  call-site retry loops can avoid double-retrying the same statuses. */
export function isTransientHTTPStatus(status: number): boolean {
  return TRANSIENT_HTTP_STATUS.has(status);
}

/** AbortSignal that fires after ms — attach to fetch so a hung request fails
 *  fast (and is retried) instead of waiting on the load balancer's idle
 *  timeout and surfacing as an opaque 504. */
export function timeoutSignal(ms: number): AbortSignal {
  return AbortSignal.timeout(ms);
}

let cachedToken: string | null = null;
let cachedTokenMintedAt = 0;

function invalidateToken(): void {
  cachedToken = null;
  cachedTokenMintedAt = 0;
}

export async function getBearer(audience: string): Promise<string> {
  if (cachedToken && Date.now() - cachedTokenMintedAt < TOKEN_REFRESH_AGE_MS) {
    return cachedToken;
  }
  const token = await core.getIDToken(audience);
  if (!token) throw new Error("OIDC mint returned empty value");
  // Mark the JWT for the runner's output filter so subsequent `core.info`,
  // error messages, or stack traces involving it print as `***`.
  core.setSecret(token);
  cachedToken = token;
  cachedTokenMintedAt = Date.now();
  return token;
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/**
 * Parse Retry-After (delay-seconds or HTTP-date) into milliseconds.
 * Returns 0 when absent or unparseable.
 */
function parseRetryAfterMs(res: Response): number {
  const raw = res.headers.get("retry-after");
  if (!raw) {
    return 0;
  }
  const asSeconds = Number(raw);
  if (Number.isFinite(asSeconds) && asSeconds >= 0) {
    return Math.min(asSeconds * 1000, MAX_RETRY_AFTER_MS);
  }
  const asDate = Date.parse(raw);
  if (!Number.isNaN(asDate)) {
    return Math.min(MAX_RETRY_AFTER_MS, Math.max(0, asDate - Date.now()));
  }
  return 0;
}

/**
 * Retry transient network failures (idle keep-alive sockets closed by a
 * load balancer during a long playwright run, brief DNS hiccups, request
 * timeouts) and transient HTTP statuses (notably 504 gateway timeouts on
 * the end-of-worker multipart shard upload). Other HTTP non-2xx responses
 * are returned to the caller verbatim so business errors (e.g.
 * RUN_NOT_IN_PROGRESS, WORKER_HAS_ACTIVE_LEASE) aren't silently masked.
 */
function isRetryableFetchError(err: unknown): boolean {
  const e = err as { name?: string; code?: string; cause?: { code?: string } };
  const code = e?.cause?.code ?? e?.code;
  return (
    e?.name === "TypeError" /* node fetch wraps net errors here */ ||
    e?.name === "TimeoutError" /* AbortSignal.timeout fired */ ||
    e?.name === "AbortError" ||
    code === "UND_ERR_SOCKET" ||
    code === "UND_ERR_HEADERS_TIMEOUT" ||
    code === "UND_ERR_BODY_TIMEOUT" ||
    code === "ECONNRESET" ||
    code === "ETIMEDOUT" ||
    code === "EAI_AGAIN" ||
    code === "ENOTFOUND"
  );
}

async function fetchWithRetry(
  makeRequest: () => Promise<Response>,
  attempts = 4,
): Promise<Response> {
  let lastErr: unknown;
  let lastRes: Response | undefined;

  for (let i = 0; i < attempts; i++) {
    try {
      const res = await makeRequest();
      if (!TRANSIENT_HTTP_STATUS.has(res.status) || i === attempts - 1) {
        return res;
      }
      lastRes = res;
      const backoffMs = 500 * 2 ** i;
      const delayMs = Math.max(backoffMs, parseRetryAfterMs(res));
      // Drain so the connection can be reused on the next attempt.
      await res.text().catch(() => undefined);
      core.info(`HTTP ${res.status} from upstream; retrying in ${delayMs}ms`);
      await sleep(delayMs);
    } catch (err) {
      lastErr = err;
      if (!isRetryableFetchError(err) || i === attempts - 1) throw err;
      const delayMs = 500 * 2 ** i;
      core.info(`fetch transient failure; retrying in ${delayMs}ms`);
      await sleep(delayMs);
    }
  }

  if (lastRes) return lastRes;
  throw lastErr;
}

/**
 * Wrap fetchWithRetry with one extra attempt on HTTP 401: invalidate
 * the cached OIDC bearer and retry. Survives transient OIDC
 * re-verification failures (clock skew, key-cache miss server-side, a
 * token that just crossed an internal validity boundary). After one
 * 401-retry, subsequent 401s are returned to the caller — a persistent
 * auth failure indicates a real config problem, not a transient blip.
 */
export async function fetchWithAuthRetry(makeRequest: () => Promise<Response>): Promise<Response> {
  let res = await fetchWithRetry(makeRequest);
  if (res.status === 401) {
    core.info("401 — invalidating cached OIDC token and retrying once");
    invalidateToken();
    res = await fetchWithRetry(makeRequest);
  }
  return res;
}

/** Status + already-read body of a request. */
export interface TextResponse {
  status: number;
  text: string;
}

/**
 * Like fetchWithRetry, but reads the response body INSIDE the retried scope.
 * fetchWithRetry returns as soon as headers arrive, so a caller that then does
 * `await res.text()` outside the loop would not retry a stall that happens
 * mid-body (the request timeout fires during the body read and escapes the
 * loop). Consuming the body here keeps `/checkout`, `/complete`, and report
 * registration resilient to a server that stalls after sending headers.
 */
async function requestTextWithRetry(
  makeRequest: () => Promise<Response>,
  attempts = 4,
): Promise<TextResponse> {
  let lastErr: unknown;
  let lastRes: TextResponse | undefined;

  for (let i = 0; i < attempts; i++) {
    try {
      const res = await makeRequest();
      if (TRANSIENT_HTTP_STATUS.has(res.status) && i < attempts - 1) {
        lastRes = { status: res.status, text: "" };
        const delayMs = Math.max(500 * 2 ** i, parseRetryAfterMs(res));
        await res.text().catch(() => undefined);
        core.info(`HTTP ${res.status} from upstream; retrying in ${delayMs}ms`);
        await sleep(delayMs);
        continue;
      }
      // Body read is inside the try so a stall here is retried, not thrown out.
      const text = await res.text();
      return { status: res.status, text };
    } catch (err) {
      lastErr = err;
      if (!isRetryableFetchError(err) || i === attempts - 1) throw err;
      const delayMs = 500 * 2 ** i;
      core.info(`fetch transient failure; retrying in ${delayMs}ms`);
      await sleep(delayMs);
    }
  }

  if (lastRes) return lastRes;
  throw lastErr;
}

/**
 * fetchTextWithAuthRetry is fetchWithAuthRetry's body-reading sibling: it
 * returns the status plus the already-consumed response text, retrying (with
 * the same 401 OIDC-refresh) including on a stall during the body read. Use it
 * for JSON endpoints whose callers need the body, so body consumption stays
 * inside the retry scope.
 */
export async function fetchTextWithAuthRetry(
  makeRequest: () => Promise<Response>,
): Promise<TextResponse> {
  let res = await requestTextWithRetry(makeRequest);
  if (res.status === 401) {
    core.info("401 — invalidating cached OIDC token and retrying once");
    invalidateToken();
    res = await requestTextWithRetry(makeRequest);
  }
  return res;
}
