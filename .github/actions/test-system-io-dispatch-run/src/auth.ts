/**
 * OIDC token caching + retrying fetch wrappers for long-lived workers.
 *
 * GH Actions OIDC tokens have a short TTL (~10 min). A worker that
 * drains a queue for an hour cannot mint once and reuse — the bearer
 * expires mid-loop and the next /complete returns 401. We mint on
 * demand, cache for a few minutes, and on a 401 we invalidate and
 * retry once. Pure transient-network failures get an exponential
 * backoff on top.
 *
 * Minting itself is retried too: the ID-token endpoint times out under
 * load (26 workers of one run died on a single `Request timeout` before
 * leasing anything), so transient mint failures back off and retry.
 * Permanent ones (missing `id-token: write`, unset request env) fail at once.
 */

import * as core from "@actions/core";

const TOKEN_REFRESH_AGE_MS = 5 * 60 * 1000;

export const OIDC_MINT_ATTEMPTS = 5;
// Sleep before attempt 2, 3, 4, 5 (about 30s total before giving up).
export const OIDC_MINT_BACKOFF_MS = [2000, 4000, 8000, 16000];

type ErrorClass = "transient" | "permanent";

/**
 * Classifies an error message from @actions/core's getIDToken. That helper
 * rethrows the HTTP failure as text (`Error Code : <status>` plus the
 * original message), so the status is parsed back out of the text.
 *
 * - HTTP 408, 429 and 5xx are transient, and so are network/timeout codes.
 * - Any other HTTP status (401/403/404: wrong permissions or endpoint) is permanent.
 * - Anything else (missing ACTIONS_ID_TOKEN_REQUEST_* env, malformed body) is
 *   permanent: retrying cannot fix a missing environment.
 */
export function classifyOIDCError(message: string): ErrorClass {
  const status = /Error Code\s*:\s*(\d{3})/.exec(message);
  if (status) {
    const code = Number(status[1]);
    if (code === 408 || code === 429 || code >= 500) return "transient";
    return "permanent";
  }
  if (
    /timeout|timed out|ETIMEDOUT|ECONNRESET|ECONNREFUSED|EAI_AGAIN|ENOTFOUND|EPIPE|socket hang up|network|fetch failed/i.test(
      message,
    )
  ) {
    return "transient";
  }
  return "permanent";
}

/** The message of a thrown value: an Error's message, or the value as text. */
function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export interface BearerProviderOptions {
  /** Mints one OIDC ID token for the audience. Injected so tests never touch the runner. */
  mint: (audience: string) => Promise<string>;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
  log?: (message: string) => void;
  attempts?: number;
  backoffMs?: readonly number[];
}

export interface BearerProvider {
  /** The cached bearer while it is fresh, else a newly minted one (concurrent misses share one mint). */
  get(audience: string): Promise<string>;
  /** Drops the cached bearer, and any mint already running, so the next get mints afresh. */
  invalidate(): void;
}

/**
 * Caches one bearer and re-mints it once it is older than TOKEN_REFRESH_AGE_MS.
 * Mint failures are retried with backoff only when classifyOIDCError calls them
 * transient; a permanent failure throws on the first attempt with a hint.
 *
 * Concurrent cache misses (parallel uploads awaiting a bearer) share one in-flight
 * mint, so a slow ID-token endpoint is asked once, not once per caller. The shared
 * mint is dropped when it settles, failure included, so the next miss retries. A
 * generation counter keeps a mint that began before invalidate() (the 401 path)
 * from writing its token into the cache.
 */
export function createBearerProvider(opts: BearerProviderOptions): BearerProvider {
  const now = opts.now ?? (() => Date.now());
  const sleep = opts.sleep ?? defaultSleep;
  const log = opts.log ?? ((m: string) => core.info(m));
  const attempts = opts.attempts ?? OIDC_MINT_ATTEMPTS;
  const backoffMs = opts.backoffMs ?? OIDC_MINT_BACKOFF_MS;

  let cachedToken: string | null = null;
  let cachedTokenMintedAt = 0;
  let inflight: Promise<string> | null = null;
  let generation = 0;

  /** Mints a bearer, backing off and retrying while the failure is transient. */
  async function mintWithRetry(audience: string): Promise<string> {
    for (let attempt = 1; ; attempt++) {
      try {
        return await opts.mint(audience);
      } catch (err) {
        const msg = errorMessage(err);
        const klass = classifyOIDCError(msg);
        if (klass === "permanent" || attempt >= attempts) {
          throw new Error(
            `OIDC token mint failed (${klass}, attempt ${attempt}/${attempts}): ${msg}` +
              (/ACTIONS_ID_TOKEN_REQUEST|Error Code\s*:\s*40[13]/.test(msg)
                ? " — the job needs `permissions: id-token: write`"
                : ""),
          );
        }
        const delayMs = backoffMs[attempt - 1] ?? backoffMs[backoffMs.length - 1] ?? 0;
        log(
          `OIDC token mint attempt ${attempt}/${attempts} failed (transient): ${msg}; ` +
            `retrying in ${delayMs}ms`,
        );
        await sleep(delayMs);
      }
    }
  }

  return {
    async get(audience: string): Promise<string> {
      if (cachedToken && now() - cachedTokenMintedAt < TOKEN_REFRESH_AGE_MS) {
        return cachedToken;
      }
      if (inflight) return inflight;
      const gen = generation;
      /** The shared mint: caches its token unless invalidate() ran after it began. */
      const mint = (async () => {
        const token = await mintWithRetry(audience);
        if (!token) throw new Error("OIDC mint returned empty value");
        if (gen === generation) {
          cachedToken = token;
          cachedTokenMintedAt = now();
        }
        return token;
      })();
      inflight = mint;
      try {
        return await mint;
      } finally {
        if (inflight === mint) inflight = null;
      }
    },
    invalidate(): void {
      generation += 1;
      inflight = null;
      cachedToken = null;
      cachedTokenMintedAt = 0;
    },
  };
}

/** Resolves after ms milliseconds. */
function defaultSleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

const defaultProvider = createBearerProvider({
  mint: async (audience) => {
    const token = await core.getIDToken(audience);
    // Mark the JWT for the runner's output filter so subsequent `core.info`,
    // error messages, or stack traces involving it print as `***`.
    core.setSecret(token);
    return token;
  },
});

/** The worker's OIDC bearer for the audience, from the process-wide cache (see createBearerProvider). */
export async function getBearer(audience: string): Promise<string> {
  return defaultProvider.get(audience);
}

/** Drops the process-wide cached bearer, so the next getBearer mints a new one. */
function invalidateToken(): void {
  defaultProvider.invalidate();
}

/** Resolves after ms milliseconds; the backoff between fetch retries. */
function sleep(ms: number): Promise<void> {
  return defaultSleep(ms);
}

/**
 * Retry transient network failures (idle keep-alive sockets closed by a
 * load balancer during a long playwright run, brief DNS hiccups, etc.).
 * Only retries on connection-level errors thrown by fetch — HTTP
 * non-2xx responses are returned to the caller verbatim so business
 * errors (e.g. RUN_NOT_IN_PROGRESS) aren't silently masked.
 */
async function fetchWithRetry(
  makeRequest: () => Promise<Response>,
  attempts = 4,
): Promise<Response> {
  let lastErr: unknown;
  for (let i = 0; i < attempts; i++) {
    try {
      return await makeRequest();
    } catch (err) {
      lastErr = err;
      const e = err as { name?: string; code?: string; cause?: { code?: string } };
      const code = e?.cause?.code ?? e?.code;
      const retryable =
        e?.name === "TypeError" /* node fetch wraps net errors here */ ||
        code === "UND_ERR_SOCKET" ||
        code === "ECONNRESET" ||
        code === "ETIMEDOUT" ||
        code === "EAI_AGAIN" ||
        code === "ENOTFOUND";
      if (!retryable || i === attempts - 1) throw err;
      const delayMs = 500 * 2 ** i;
      core.info(`fetch transient failure (${code ?? e.name}); retrying in ${delayMs}ms`);
      await sleep(delayMs);
    }
  }
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
