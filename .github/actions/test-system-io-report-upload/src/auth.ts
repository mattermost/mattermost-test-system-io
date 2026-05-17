/**
 * OIDC token caching + retrying fetch wrappers.
 *
 * GH Actions OIDC tokens have a short TTL (~10 min). The upload pipeline
 * (begin → register → upload×2) can take longer than that on slow CI
 * runners or large screenshot uploads, so we mint on demand, cache for a
 * few minutes, and on a 401 we invalidate and retry once. Pure transient
 * network failures get an exponential backoff on top.
 */

import * as core from "@actions/core";

const TOKEN_REFRESH_AGE_MS = 5 * 60 * 1000;

let cachedToken: string | null = null;
let cachedTokenMintedAt = 0;
// Tracking the audience the cached token was minted for prevents a second
// getBearer(otherAudience) call from reusing a token whose `aud` claim won't
// match — the server would reject it with 401 even though caching "worked".
let cachedTokenAudience: string | null = null;

function invalidateToken(): void {
  cachedToken = null;
  cachedTokenMintedAt = 0;
  cachedTokenAudience = null;
}

export async function getBearer(audience: string): Promise<string> {
  if (
    cachedToken &&
    cachedTokenAudience === audience &&
    Date.now() - cachedTokenMintedAt < TOKEN_REFRESH_AGE_MS
  ) {
    return cachedToken;
  }
  const token = await core.getIDToken(audience);
  if (!token) throw new Error("OIDC mint returned empty value");
  // Mark the JWT for the runner's output filter so subsequent `core.info`,
  // error messages, or stack traces involving it print as `***`.
  core.setSecret(token);
  cachedToken = token;
  cachedTokenAudience = audience;
  cachedTokenMintedAt = Date.now();
  return token;
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/**
 * Retry transient network failures (idle keep-alive sockets closed by a
 * load balancer during a long upload, brief DNS hiccups, etc.). Only
 * retries on connection-level errors thrown by fetch — HTTP non-2xx
 * responses are returned to the caller verbatim so business errors
 * aren't silently masked.
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
 * Wrap fetchWithRetry with one extra attempt on HTTP 401: invalidate the
 * cached OIDC bearer and retry. Survives transient OIDC re-verification
 * failures (clock skew, key-cache miss server-side, a token that just
 * crossed an internal validity boundary). After one 401-retry, subsequent
 * 401s are returned to the caller — a persistent auth failure indicates
 * a real config problem, not a transient blip.
 */
export async function fetchWithAuthRetry(makeRequest: () => Promise<Response>): Promise<Response> {
  let res = await fetchWithRetry(makeRequest);
  if (res.status === 401) {
    core.info("401 — invalidating cached OIDC token and retrying once");
    // Release the unread 401 body so undici can return the socket to the
    // pool instead of holding it open until GC.
    await res.body?.cancel();
    invalidateToken();
    res = await fetchWithRetry(makeRequest);
  }
  return res;
}
