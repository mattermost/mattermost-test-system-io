import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  classifyOIDCError,
  createBearerProvider,
  OIDC_MINT_ATTEMPTS,
  OIDC_MINT_BACKOFF_MS,
} from "./auth.ts";

const TIMEOUT_MSG =
  "Error message: Failed to get ID Token. \n \n         Error Code : undefined\n \n         Error Message: Request timeout";

test("classifyOIDCError: timeouts, 408/429/5xx and network codes are transient", () => {
  assert.equal(classifyOIDCError(TIMEOUT_MSG), "transient");
  assert.equal(
    classifyOIDCError("Error Code : 503\n Error Message: Service Unavailable"),
    "transient",
  );
  assert.equal(classifyOIDCError("Error Code : 500"), "transient");
  assert.equal(classifyOIDCError("Error Code : 429"), "transient");
  assert.equal(classifyOIDCError("Error Code : 408"), "transient");
  assert.equal(classifyOIDCError("connect ECONNRESET 10.0.0.1:443"), "transient");
  assert.equal(classifyOIDCError("getaddrinfo EAI_AGAIN token.actions"), "transient");
});

test("classifyOIDCError: missing request env, auth/404 HTTP errors and odd bodies are permanent", () => {
  assert.equal(
    classifyOIDCError("Error message: Unable to get ACTIONS_ID_TOKEN_REQUEST_URL env variable"),
    "permanent",
  );
  assert.equal(classifyOIDCError("Error Code : 401\n Error Message: Unauthorized"), "permanent");
  assert.equal(classifyOIDCError("Error Code : 403"), "permanent");
  assert.equal(classifyOIDCError("Error Code : 404"), "permanent");
  assert.equal(
    classifyOIDCError("Error message: Response json body do not have ID Token field"),
    "permanent",
  );
});

test("mint retry: transient timeouts back off 2s/4s and then succeed, logging each retry", async () => {
  let calls = 0;
  const sleeps: number[] = [];
  const logs: string[] = [];
  const provider = createBearerProvider({
    mint: async () => {
      calls += 1;
      if (calls <= 2) throw new Error(TIMEOUT_MSG);
      return "jwt-1";
    },
    sleep: async (ms) => {
      sleeps.push(ms);
    },
    log: (m) => logs.push(m),
  });

  assert.equal(await provider.get("aud"), "jwt-1");
  assert.equal(calls, 3);
  assert.deepEqual(sleeps, [2000, 4000]);
  assert.equal(logs.length, 2);
  assert.match(logs[0]!, /attempt 1\/5 failed \(transient\).*Request timeout.*retrying in 2000ms/s);
  assert.match(logs[1]!, /attempt 2\/5 failed \(transient\).*retrying in 4000ms/s);
});

test("mint retry: gives up after 5 attempts with the backoff schedule 2/4/8/16s", async () => {
  let calls = 0;
  const sleeps: number[] = [];
  const provider = createBearerProvider({
    mint: async () => {
      calls += 1;
      throw new Error(TIMEOUT_MSG);
    },
    sleep: async (ms) => {
      sleeps.push(ms);
    },
    log: () => {},
  });

  await assert.rejects(provider.get("aud"), (err: Error) => {
    assert.match(err.message, /transient, attempt 5\/5\)/);
    assert.match(err.message, /Request timeout/);
    return true;
  });
  assert.equal(calls, OIDC_MINT_ATTEMPTS);
  assert.equal(OIDC_MINT_ATTEMPTS, 5);
  assert.deepEqual(sleeps, [2000, 4000, 8000, 16000]);
  assert.deepEqual([...OIDC_MINT_BACKOFF_MS], [2000, 4000, 8000, 16000]);
});

test("mint retry: a missing id-token permission fails on the first attempt with a hint", async () => {
  let calls = 0;
  let slept = false;
  const provider = createBearerProvider({
    mint: async () => {
      calls += 1;
      throw new Error("Error message: Unable to get ACTIONS_ID_TOKEN_REQUEST_URL env variable");
    },
    sleep: async () => {
      slept = true;
    },
    log: () => {},
  });

  await assert.rejects(provider.get("aud"), (err: Error) => {
    assert.match(err.message, /permanent, attempt 1\/5\)/);
    assert.match(err.message, /permissions: id-token: write/);
    return true;
  });
  assert.equal(calls, 1);
  assert.equal(slept, false);
});

test("bearer cache: reuses a token younger than 5 minutes and re-mints once it is older", async () => {
  let now = 0;
  let calls = 0;
  const provider = createBearerProvider({
    mint: async () => {
      calls += 1;
      return `jwt-${calls}`;
    },
    now: () => now,
    sleep: async () => {},
    log: () => {},
  });

  assert.equal(await provider.get("aud"), "jwt-1");
  now = 4 * 60 * 1000;
  assert.equal(await provider.get("aud"), "jwt-1");
  assert.equal(calls, 1);
  now = 5 * 60 * 1000;
  assert.equal(await provider.get("aud"), "jwt-2");
  assert.equal(calls, 2);
});

test("bearer cache: invalidate forces a fresh mint (the 401 path)", async () => {
  let calls = 0;
  const provider = createBearerProvider({
    mint: async () => {
      calls += 1;
      return `jwt-${calls}`;
    },
    now: () => 0,
    sleep: async () => {},
    log: () => {},
  });

  assert.equal(await provider.get("aud"), "jwt-1");
  provider.invalidate();
  assert.equal(await provider.get("aud"), "jwt-2");
});

test("bearer cache: an empty token is an error, not cached", async () => {
  const provider = createBearerProvider({
    mint: async () => "",
    sleep: async () => {},
    log: () => {},
  });
  await assert.rejects(provider.get("aud"), /OIDC mint returned empty value/);
});

/** A promise and the functions that settle it, so a test controls when a mint finishes. */
function deferred<T>(): {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
} {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test("bearer cache: concurrent misses share one in-flight mint", async () => {
  let calls = 0;
  const gate = deferred<string>();
  const provider = createBearerProvider({
    mint: async () => {
      calls += 1;
      return gate.promise;
    },
    now: () => 0,
    sleep: async () => {},
    log: () => {},
  });
  const both = Promise.all([provider.get("aud"), provider.get("aud")]);
  gate.resolve("jwt-1");
  assert.deepEqual(await both, ["jwt-1", "jwt-1"]);
  assert.equal(calls, 1);
  // The shared mint was cached: a later get does not mint again.
  assert.equal(await provider.get("aud"), "jwt-1");
  assert.equal(calls, 1);
});

test("bearer cache: a failed shared mint is dropped, so the next get mints again", async () => {
  let calls = 0;
  const provider = createBearerProvider({
    mint: async () => {
      calls += 1;
      if (calls === 1) throw new Error("Error Code : 403");
      return "jwt-2";
    },
    now: () => 0,
    sleep: async () => {},
    log: () => {},
  });
  const results = await Promise.allSettled([provider.get("aud"), provider.get("aud")]);
  assert.deepEqual(
    results.map((r) => r.status),
    ["rejected", "rejected"],
  );
  assert.equal(calls, 1);
  assert.equal(await provider.get("aud"), "jwt-2");
  assert.equal(calls, 2);
});

test("bearer cache: a mint that began before invalidate does not repopulate the cache", async () => {
  const gates = [deferred<string>(), deferred<string>()];
  let calls = 0;
  const provider = createBearerProvider({
    mint: async () => gates[calls++]!.promise,
    now: () => 0,
    sleep: async () => {},
    log: () => {},
  });
  const stale = provider.get("aud");
  provider.invalidate();
  const fresh = provider.get("aud");
  assert.equal(calls, 2, "the get after invalidate starts its own mint");
  gates[0]!.resolve("jwt-old");
  gates[1]!.resolve("jwt-new");
  assert.equal(await stale, "jwt-old");
  assert.equal(await fresh, "jwt-new");
  // Only the mint started after invalidate was cached.
  assert.equal(await provider.get("aud"), "jwt-new");
  assert.equal(calls, 2);
});
