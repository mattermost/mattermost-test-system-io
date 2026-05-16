#!/usr/bin/env node
/**
 * Upload seed data to the report server using the stateless API.
 *
 * API flow (per report):
 *   beginReport(total_reports_expected) -> for each shard: registerReport() -> uploadJson()
 *
 * The report group auto-finalizes server-side once `total_reports_expected`
 * shards reach `complete`. With --incomplete, the script uploads fewer
 * shards than declared so the group stays `in_progress` (and the staleness
 * reaper will eventually flip it to `incomplete`).
 *
 * Authentication: Uses mock OIDC (Bearer tokens) by default. Each shard gets its
 * own signed JWT with a unique `check_run_id` claim, replicating how GitHub
 * Actions OIDC works in production.
 *
 * Prerequisites:
 *   The API server must be started with OIDC enabled and pointing at the mock
 *   JWKS server this script starts on port 9090:
 *
 *     TSIO_GITHUB_OIDC_ENABLED=true \
 *     TSIO_GITHUB_OIDC_ISSUER=http://localhost:9090 \
 *     make dev
 *
 * Usage:
 *   node scripts/upload-seed.js                          # Upload all seed dirs (branch=main)
 *   node scripts/upload-seed.js --branch main            # Upload all seed dirs for branch "main"
 *   node scripts/upload-seed.js --branch release-9.11    # Upload all seed dirs for release branch
 *   node scripts/upload-seed.js --branch pr-1234         # Upload all seed dirs for a pull request
 *   node scripts/upload-seed.js --pr 1234                # Same as --branch pr-1234 (shortcut)
 *   node scripts/upload-seed.js --branch master           # Upload all seed dirs for "master"
 *   node scripts/upload-seed.js --incomplete              # Skip last shard + don't complete (in_progress)
 *   node scripts/upload-seed.js --commit <sha>            # Rerun on an existing commit (new run ID)
 *   node scripts/upload-seed.js --image <docker-image>   # Set server image in environment metadata
 *   node scripts/upload-seed.js --name playwright-enterprise  # Use custom report name (default: framework)
 *   node scripts/upload-seed.js seed/cypress-ci cypress   # Upload specific seed dir + framework
 *
 * Each invocation generates a unique commit SHA and run ID so the script can
 * be run multiple times to populate different reports.
 *
 * Environment variables:
 *   API_BASE       - Base URL (default: https://localhost:8443/api/v1)
 *   TSIO_API_KEY   - API key for authentication (falls back to admin key)
 *   TSIO_ADMIN_KEY - Admin key for OIDC policy setup (default: dev-admin-key-do-not-use-in-production)
 *   BATCH_SIZE     - Number of files per upload batch (default: 50)
 *   MOCK_OIDC_PORT - Port for mock JWKS server (default: 9090)
 *   MOCK_OIDC_AUDIENCE - aud claim minted into tokens (default: tsio). Must
 *                        match the server's TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE.
 */

const fs = require("fs");
const path = require("path");
const http = require("http");
const https = require("https");
const crypto = require("crypto");

// Local-dev helper: accept the mkcert-issued self-signed cert that tsio
// serves at https://localhost:8443. Node uses its bundled CA list (not the
// OS keychain), so mkcert -install on the host doesn't reach this process.
// Setting NODE_TLS_REJECT_UNAUTHORIZED=0 is process-local and only affects
// this script. Override in the environment if you point API_BASE at a host
// whose cert chains to a public CA.
if (!process.env.NODE_TLS_REJECT_UNAUTHORIZED) {
  process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
}

// Configuration
const API_BASE = process.env.API_BASE || "https://localhost:8443/api/v1";
const API_KEY = process.env.TSIO_API_KEY;
const ADMIN_KEY =
  process.env.TSIO_ADMIN_KEY || "dev-admin-key-do-not-use-in-production";
const BATCH_SIZE = parseInt(process.env.BATCH_SIZE || "50", 10);
const MOCK_OIDC_PORT = parseInt(process.env.MOCK_OIDC_PORT || "9090", 10);
const MOCK_OIDC_AUDIENCE = process.env.MOCK_OIDC_AUDIENCE || "tsio";

/**
 * Parse --branch argument from CLI args.
 * Accepted formats: main, master, release-*, pr-<number>
 * Returns OIDC-compatible claim values.
 *
 * For branches: ref=refs/heads/<branch>, head_ref/base_ref unset
 * For PRs:      ref=refs/pull/<n>/merge, head_ref=<generated>, base_ref=main
 *
 * `--pr <n>` is a shortcut for `--branch pr-<n>` so callers don't have to
 * remember the prefix convention. The two forms are equivalent; --pr wins
 * if both are passed.
 */
function parseBranchArg(args) {
  let branch = "main"; // default
  const branchIdx = args.indexOf("--branch");
  if (branchIdx !== -1 && branchIdx + 1 < args.length) {
    branch = args[branchIdx + 1];
  }
  const prIdx = args.indexOf("--pr");
  if (prIdx !== -1 && prIdx + 1 < args.length) {
    const n = parseInt(args[prIdx + 1], 10);
    if (!Number.isFinite(n) || n <= 0) {
      console.error(`--pr expects a positive integer, got: ${args[prIdx + 1]}`);
      process.exit(2);
    }
    branch = `pr-${n}`;
  }

  const prMatch = branch.match(/^pr-(\d+)$/);
  if (prMatch) {
    return {
      branch,
      ref: `refs/pull/${prMatch[1]}/merge`,
      head_ref: `refs/heads/pr-${prMatch[1]}-branch`,
      base_ref: "refs/heads/main",
      ref_type: "branch",
      event_name: "pull_request",
      pr_number: prMatch[1],
    };
  }

  return {
    branch,
    ref: `refs/heads/${branch}`,
    head_ref: null,
    base_ref: null,
    ref_type: "branch",
    event_name: "push",
    pr_number: null,
  };
}

/**
 * Generate a random 40-char hex commit SHA.
 */
function generateCommitSha() {
  return crypto.randomBytes(20).toString("hex");
}

/**
 * Generate a numeric run ID (mimics GitHub Actions run_id).
 * Produces a 14-digit number starting with "2423" that increments on each call,
 * based on epoch time so successive script invocations yield ascending IDs.
 */
let _runIdCounter = 0;
function generateRunId() {
  const epochMs = Date.now() + _runIdCounter++;
  // "2423" prefix + last 10 digits of epoch ms = 14-digit realistic run ID
  return `2423${epochMs.toString().slice(-10)}`;
}

/**
 * Parse --commit argument from CLI args.
 * If provided, reuses the given SHA; otherwise generates a random one.
 */
function parseCommitArg(args) {
  const idx = args.indexOf("--commit");
  if (idx !== -1 && idx + 1 < args.length) {
    return args[idx + 1];
  }
  return null;
}

/**
 * Parse --image argument from CLI args.
 * If provided, sets the server image in environment metadata.
 */
function parseImageArg(args) {
  const idx = args.indexOf("--image");
  if (idx !== -1 && idx + 1 < args.length) {
    return args[idx + 1];
  }
  return null;
}

/**
 * Parse --name argument from CLI args.
 * If provided, uses the given name for report grouping.
 * Defaults to framework value when not provided.
 */
function parseNameArg(args) {
  const idx = args.indexOf("--name");
  if (idx !== -1 && idx + 1 < args.length) {
    return args[idx + 1];
  }
  return null;
}

// Dynamic seed context (unique per invocation)
const SEED_BRANCH_INFO = parseBranchArg(process.argv.slice(2));
const SEED_IMAGE = parseImageArg(process.argv.slice(2));
const SEED_NAME = parseNameArg(process.argv.slice(2));
const SEED_CONTEXT = {
  repository: "mattermost/mattermost",
  commit: parseCommitArg(process.argv.slice(2)) || generateCommitSha(),
  gh_run_id: generateRunId(),
};

// Default seed configurations matching actual seed/ directory structure.
// The name is a constant user-defined identifier for the test configuration
// (tool + scope + edition). It does not change per branch or PR.
const seedConfigs = [
  { dir: "seed/cypress-ci", framework: "cypress", name: "cypress-full-enterprise" },
  { dir: "seed/playwright-ci", framework: "playwright", name: "playwright-full-enterprise" },
];

// Files/directories to exclude from uploads
const EXCLUDE_PATTERNS = [".DS_Store"];
const VIDEO_EXTENSIONS = [".mp4", ".webm", ".avi", ".mov", ".mkv"];

// Allowed image extensions for screenshot uploads
const IMAGE_EXTENSIONS = [".png", ".jpg", ".jpeg", ".gif", ".webp"];

// ── Mock OIDC Provider ───────────────────────────────────────────────────────

/**
 * Minimal JWT signer using Node.js built-in crypto (no external dependencies).
 */
function base64url(input) {
  const buf = typeof input === "string" ? Buffer.from(input) : input;
  return buf.toString("base64url");
}

function signJwt(payload, privateKey, kid) {
  const header = { alg: "RS256", typ: "JWT", kid };
  const headerB64 = base64url(JSON.stringify(header));
  const payloadB64 = base64url(JSON.stringify(payload));
  const signingInput = `${headerB64}.${payloadB64}`;
  const signature = crypto.sign("sha256", Buffer.from(signingInput), privateKey);
  return `${signingInput}.${base64url(signature)}`;
}

/**
 * Mock OIDC provider that starts a local JWKS server and issues signed JWTs.
 * Implemented with Node.js built-in crypto to keep the seed script dependency-free.
 */
class MockOidcProvider {
  constructor() {
    this.kid = "seed-key-1";
    this.port = MOCK_OIDC_PORT;
    this.issuer = `http://localhost:${this.port}`;
    this.server = null;
    this.publicKey = null;
    this.privateKey = null;
  }

  /**
   * Load or generate RSA key pair and start the JWKS HTTP server.
   * Keys are persisted to .oidc-keys.json so the server's JWKS cache
   * stays valid across multiple seed script invocations.
   */
  async start() {
    const keysPath = path.join(__dirname, ".oidc-keys.json");
    let publicKey, privateKey;

    if (fs.existsSync(keysPath)) {
      const saved = JSON.parse(fs.readFileSync(keysPath, "utf-8"));
      publicKey = crypto.createPublicKey({ key: saved.public, format: "pem" });
      privateKey = crypto.createPrivateKey({ key: saved.private, format: "pem" });
      console.log("  Loaded existing OIDC keys from .oidc-keys.json");
    } else {
      const pair = crypto.generateKeyPairSync("rsa", { modulusLength: 2048 });
      publicKey = pair.publicKey;
      privateKey = pair.privateKey;
      fs.writeFileSync(keysPath, JSON.stringify({
        public: publicKey.export({ type: "spki", format: "pem" }),
        private: privateKey.export({ type: "pkcs8", format: "pem" }),
      }));
      console.log("  Generated new OIDC keys → .oidc-keys.json");
    }

    this.publicKey = publicKey;
    this.privateKey = privateKey;

    // Export public key as JWK for the JWKS endpoint
    const jwk = publicKey.export({ format: "jwk" });

    const jwksResponse = JSON.stringify({
      keys: [
        {
          kty: jwk.kty,
          n: jwk.n,
          e: jwk.e,
          kid: this.kid,
          alg: "RS256",
          use: "sig",
        },
      ],
    });

    // Start HTTP server serving /.well-known/jwks
    this.server = http.createServer((req, res) => {
      if (req.url === "/.well-known/jwks") {
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(jwksResponse);
      } else {
        res.writeHead(404);
        res.end();
      }
    });

    await new Promise((resolve, reject) => {
      this.server.on("error", reject);
      this.server.listen(this.port, () => {
        console.log(`  Mock OIDC JWKS server listening on ${this.issuer}/.well-known/jwks`);
        resolve();
      });
    });
  }

  /**
   * Issue a signed JWT with the given per-shard claims.
   *
   * @param {object} overrides - Claim overrides (e.g., check_run_id)
   * @returns {string} Signed JWT string
   */
  issueToken(overrides = {}) {
    const now = Math.floor(Date.now() / 1000);
    const payload = {
      // GitHub OIDC identity claims
      sub: `repo:${SEED_CONTEXT.repository}:ref:${SEED_BRANCH_INFO.ref}`,
      repository: SEED_CONTEXT.repository,
      repository_owner: "mattermost",
      actor: "seed-script",

      // Git ref claims
      sha: SEED_CONTEXT.commit,
      ref: SEED_BRANCH_INFO.ref,
      ref_type: SEED_BRANCH_INFO.ref_type,
      // head_ref/base_ref are set for pull_request events
      ...(SEED_BRANCH_INFO.head_ref ? { head_ref: SEED_BRANCH_INFO.head_ref } : {}),
      ...(SEED_BRANCH_INFO.base_ref ? { base_ref: SEED_BRANCH_INFO.base_ref } : {}),

      // Workflow / run claims
      workflow: "E2E Tests",
      event_name: SEED_BRANCH_INFO.event_name,
      run_id: SEED_CONTEXT.gh_run_id,
      run_number: "1",
      run_attempt: "1",

      // Environment / runner claims
      runner_environment: "github-hosted",

      // Per-job override (check_run_id should be unique per job)
      ...overrides,

      // Standard JWT fields (always last so they cannot be overridden)
      iss: this.issuer,
      aud: MOCK_OIDC_AUDIENCE,
      iat: now,
      exp: now + 600, // 10 minutes
      nbf: now,
    };

    return signJwt(payload, this.privateKey, this.kid);
  }

  /**
   * Stop the JWKS server.
   */
  async stop() {
    if (this.server) {
      await new Promise((resolve) => {
        this.server.close(resolve);
      });
      this.server = null;
      console.log("  Mock OIDC JWKS server stopped.");
    }
  }
}

// ── Utility helpers ──────────────────────────────────────────────────────────

/**
 * Check if file should be excluded from upload.
 */
function shouldExclude(filepath) {
  for (const pattern of EXCLUDE_PATTERNS) {
    if (filepath.includes(pattern)) {
      return true;
    }
  }

  const lowerPath = filepath.toLowerCase();
  for (const ext of VIDEO_EXTENSIONS) {
    if (lowerPath.endsWith(ext)) {
      return true;
    }
  }

  return false;
}

/**
 * Get MIME type for a filename.
 */
function getMimeType(filename) {
  const ext = path.extname(filename).toLowerCase().slice(1);
  const mimeTypes = {
    html: "text/html",
    json: "application/json",
    xml: "application/xml",
    css: "text/css",
    js: "application/javascript",
    png: "image/png",
    jpg: "image/jpeg",
    jpeg: "image/jpeg",
    gif: "image/gif",
    webp: "image/webp",
    svg: "image/svg+xml",
    woff: "font/woff",
    woff2: "font/woff2",
    txt: "text/plain",
    md: "text/markdown",
  };
  return mimeTypes[ext] || "application/octet-stream";
}

/**
 * Recursively get all files in a directory.
 */
function getAllFiles(dirPath, baseDir = dirPath) {
  const files = [];

  if (!fs.existsSync(dirPath)) {
    return files;
  }

  const entries = fs.readdirSync(dirPath, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      files.push(...getAllFiles(fullPath, baseDir));
    } else if (entry.isFile()) {
      const relativePath = path.relative(baseDir, fullPath);
      if (!shouldExclude(relativePath)) {
        const stats = fs.statSync(fullPath);
        files.push({
          fullPath,
          relativePath,
          size: stats.size,
          contentType: getMimeType(relativePath),
        });
      }
    }
  }

  return files;
}

/**
 * Recursively find all JSON files in a directory.
 */
function findJsonFiles(dirPath, baseDir = dirPath) {
  const files = [];

  if (!fs.existsSync(dirPath)) {
    return files;
  }

  const entries = fs.readdirSync(dirPath, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      files.push(...findJsonFiles(fullPath, baseDir));
    } else if (entry.isFile() && entry.name.endsWith(".json")) {
      const relativePath = path.relative(baseDir, fullPath);
      const stats = fs.statSync(fullPath);
      files.push({
        fullPath,
        relativePath,
        size: stats.size,
        contentType: "application/json",
      });
    }
  }

  return files;
}

/**
 * Check if file is an allowed image for screenshot uploads.
 */
function isImageFile(filepath) {
  const ext = path.extname(filepath).toLowerCase();
  return IMAGE_EXTENSIONS.includes(ext);
}

/**
 * Get all screenshot image files from a screenshots directory.
 */
function getScreenshotFiles(screenshotsDir) {
  const allFiles = getAllFiles(screenshotsDir);
  return allFiles.filter(
    (f) => isImageFile(f.relativePath) && !shouldExclude(f.relativePath),
  );
}

// ── HTTP helpers ─────────────────────────────────────────────────────────────

/**
 * Build admin auth headers. Used only by the OIDC policy bootstrap call,
 * which requires the X-Admin-Key header — the OpenAPI validator rejects
 * X-API-Key on this endpoint with a 400. Send X-Admin-Key unconditionally
 * even when TSIO_API_KEY is also set in the env.
 */
function getAdminAuthHeaders() {
  return { "X-Admin-Key": ADMIN_KEY };
}

/**
 * Build Bearer auth headers for a specific OIDC token.
 */
function getBearerAuthHeaders(token) {
  return { Authorization: `Bearer ${token}` };
}

/**
 * Make HTTP request with JSON body.
 */
function makeRequest(url, options, body) {
  return new Promise((resolve, reject) => {
    const parsedUrl = new URL(url);
    const client = parsedUrl.protocol === "https:" ? https : http;

    const req = client.request(
      {
        hostname: parsedUrl.hostname,
        port: parsedUrl.port || (parsedUrl.protocol === "https:" ? 443 : 80),
        path: parsedUrl.pathname + parsedUrl.search,
        method: options.method || "POST",
        headers: options.headers,
        timeout: 300000,
      },
      (res) => {
        let data = Buffer.alloc(0);
        res.on("data", (chunk) => {
          data = Buffer.concat([data, chunk]);
        });
        res.on("end", () => {
          resolve({
            statusCode: res.statusCode,
            body: data.toString("utf-8"),
          });
        });
      },
    );

    req.on("error", reject);
    req.on("timeout", () => {
      req.destroy();
      reject(new Error("Request timeout"));
    });

    if (body) {
      req.write(body);
    }
    req.end();
  });
}

/**
 * Upload files via multipart/form-data.
 *
 * @param {string} url - Upload endpoint URL
 * @param {Array} files - Files to upload
 * @param {object} authHeaders - Auth headers (Bearer token or admin key)
 */
function uploadFilesMultipart(url, files, authHeaders) {
  return new Promise((resolve, reject) => {
    const parsedUrl = new URL(url);
    const client = parsedUrl.protocol === "https:" ? https : http;
    const boundary = `----FormBoundary${crypto.randomBytes(16).toString("hex")}`;

    // Build multipart body
    const parts = [];
    for (const file of files) {
      const content = fs.readFileSync(file.fullPath);
      parts.push(
        Buffer.from(
          `--${boundary}\r\n` +
            `Content-Disposition: form-data; name="files"; filename="${file.relativePath}"\r\n` +
            `Content-Type: ${file.contentType}\r\n\r\n`,
        ),
      );
      parts.push(content);
      parts.push(Buffer.from("\r\n"));
    }
    parts.push(Buffer.from(`--${boundary}--\r\n`));

    const body = Buffer.concat(parts);

    const headers = {
      ...authHeaders,
      "Content-Type": `multipart/form-data; boundary=${boundary}`,
      "Content-Length": body.length,
    };

    const req = client.request(
      {
        hostname: parsedUrl.hostname,
        port: parsedUrl.port || (parsedUrl.protocol === "https:" ? 443 : 80),
        path: parsedUrl.pathname + parsedUrl.search,
        method: "POST",
        headers,
        timeout: 300000,
      },
      (res) => {
        let data = Buffer.alloc(0);
        res.on("data", (chunk) => {
          data = Buffer.concat([data, chunk]);
        });
        res.on("end", () => {
          resolve({
            statusCode: res.statusCode,
            body: data.toString("utf-8"),
          });
        });
      },
    );

    req.on("error", reject);
    req.on("timeout", () => {
      req.destroy();
      reject(new Error("Upload timeout"));
    });

    req.write(body);
    req.end();
  });
}

// ── OIDC policy setup ────────────────────────────────────────────────────────

/**
 * Create an OIDC policy allowing the seed repository via admin key.
 * This is a one-time setup step; if the policy already exists the server
 * will accept a duplicate (idempotent by pattern).
 */
async function createOidcPolicy() {
  const url = `${API_BASE}/admin/oidc-policies`;
  const body = JSON.stringify({
    repository_pattern: "mattermost/*",
    role: "contributor",
    description: "Seed script: allow all mattermost repos via OIDC",
  });

  const headers = {
    ...getAdminAuthHeaders(),
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode === 201 || response.statusCode === 200) {
    const result = JSON.parse(response.body);
    console.log(`  OIDC policy created: ${result.id} (pattern: mattermost/*)`);
    return result;
  }

  // Policy may already exist — log but don't fail
  console.log(
    `  OIDC policy setup returned ${response.statusCode}: ${response.body}`,
  );
  return null;
}

// ── API calls (stateless flow with per-shard Bearer tokens) ────────────────────

/**
 * POST /api/v1/reports/begin
 * Begins a report session for the given repository/commit/run/framework.
 * total_reports_expected is required (>0) and frozen on first call; later
 * begins with a different value would return 409 EXPECTED_REPORTS_MISMATCH.
 */
async function beginReport(context, totalReportsExpected, authHeaders) {
  const url = `${API_BASE}/reports/begin`;
  const payload = {
    repository: context.repository,
    commit: context.commit,
    gh_run_id: context.gh_run_id,
    framework: context.framework,
    name: context.name,
    total_reports_expected: totalReportsExpected,
  };
  if (context.gh_pr_number !== undefined) {
    payload.gh_pr_number = context.gh_pr_number;
  }
  const body = JSON.stringify(payload);

  const headers = {
    ...authHeaders,
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode !== 200 && response.statusCode !== 201) {
    throw new Error(
      `Failed to begin report (${response.statusCode}): ${response.body}`,
    );
  }

  return JSON.parse(response.body);
}

/**
 * POST /api/v1/reports/register
 * Registers an upload within a report, declaring the JSON files it will upload.
 */
async function registerReport(context, ghJobId, ghJobName, jsonFiles, screenshotFiles, authHeaders, environmentMetadata) {
  const url = `${API_BASE}/reports/register`;
  const payload = {
    repository: context.repository,
    commit: context.commit,
    gh_run_id: context.gh_run_id,
    framework: context.framework,
    name: context.name,
    branch: context.branch,
    gh_job_id: ghJobId,
    gh_job_name: ghJobName,
    json_files: jsonFiles.map((f) => ({
      path: f.relativePath,
      size: f.size,
    })),
    screenshots: screenshotFiles.map((f) => ({
      path: f.relativePath,
      size: f.size,
    })),
  };
  if (environmentMetadata) {
    payload.environment_metadata = environmentMetadata;
  }
  if (context.gh_pr_number !== undefined) {
    payload.gh_pr_number = context.gh_pr_number;
  }
  const body = JSON.stringify(payload);

  const headers = {
    ...authHeaders,
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode !== 200 && response.statusCode !== 201) {
    throw new Error(
      `Failed to register report (${response.statusCode}): ${response.body}`,
    );
  }

  return JSON.parse(response.body);
}

/**
 * Upload JSON files to POST /reports/upload/{reportId}/{uploadId}/json
 */
async function uploadJsonFiles(reportId, uploadId, files, authHeaders) {
  const url = `${API_BASE}/reports/upload/${reportId}/${uploadId}/json`;
  let totalUploaded = 0;

  for (let i = 0; i < files.length; i += BATCH_SIZE) {
    const batch = files.slice(i, i + BATCH_SIZE);
    const batchNum = Math.floor(i / BATCH_SIZE) + 1;
    const totalBatches = Math.ceil(files.length / BATCH_SIZE);

    if (totalBatches > 1) {
      console.log(
        `      Batch ${batchNum}/${totalBatches}: uploading ${batch.length} JSON files...`,
      );
    }

    const response = await uploadFilesMultipart(url, batch, authHeaders);

    if (response.statusCode !== 200) {
      throw new Error(
        `Failed to upload JSON files (${response.statusCode}): ${response.body}`,
      );
    }

    const result = JSON.parse(response.body);
    totalUploaded += result.files_uploaded || batch.length;
  }

  return totalUploaded;
}

/**
 * Upload screenshot files to POST /reports/upload/{reportId}/{uploadId}/screenshots
 */
async function uploadScreenshots(reportId, uploadId, files, authHeaders) {
  const url = `${API_BASE}/reports/upload/${reportId}/${uploadId}/screenshots`;
  let totalUploaded = 0;

  for (let i = 0; i < files.length; i += BATCH_SIZE) {
    const batch = files.slice(i, i + BATCH_SIZE);
    const batchNum = Math.floor(i / BATCH_SIZE) + 1;
    const totalBatches = Math.ceil(files.length / BATCH_SIZE);

    if (totalBatches > 1) {
      console.log(
        `      Batch ${batchNum}/${totalBatches}: uploading ${batch.length} screenshots...`,
      );
    }

    const response = await uploadFilesMultipart(url, batch, authHeaders);

    if (response.statusCode !== 200) {
      throw new Error(
        `Failed to upload screenshots (${response.statusCode}): ${response.body}`,
      );
    }

    const result = JSON.parse(response.body);
    totalUploaded += result.files_uploaded || batch.length;
  }

  return totalUploaded;
}

// ── Shard discovery ──────────────────────────────────────────────────────────

/**
 * Detect shard directories within a seed directory.
 *
 * Cypress:     cypress-full--results-*
 * Playwright:  playwright-full--results-* and playwright-full--retest-*
 */
function getShardDirectories(seedDir, framework) {
  if (!fs.existsSync(seedDir) || !fs.statSync(seedDir).isDirectory()) {
    return [];
  }

  const entries = fs.readdirSync(seedDir, { withFileTypes: true });
  const shards = [];

  for (const entry of entries) {
    if (!entry.isDirectory() || entry.name.startsWith(".")) {
      continue;
    }

    let matches = false;
    if (framework === "cypress") {
      matches = entry.name.match(/^cypress-full--results-\d+$/) !== null;
    } else if (framework === "playwright") {
      matches =
        entry.name.match(/^playwright-full--results-\d+$/) !== null ||
        entry.name.match(/^playwright-full--retest-/) !== null;
    }

    if (matches) {
      shards.push({
        name: entry.name,
        path: path.join(seedDir, entry.name),
      });
    }
  }

  // Sort for consistent ordering
  shards.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
  return shards;
}

/**
 * Find JSON files for a shard based on framework.
 *
 * Cypress:     results/mochawesome-report/json/ (recursive .json files)
 * Playwright:  results/reporter/ (contains results.json)
 */
function findShardJsonFiles(shardDir, framework) {
  if (framework === "cypress") {
    const jsonDir = path.join(
      shardDir,
      "results",
      "mochawesome-report",
      "json",
    );
    return findJsonFiles(jsonDir, jsonDir);
  } else if (framework === "playwright") {
    const reporterDir = path.join(shardDir, "results", "reporter");
    return findJsonFiles(reporterDir, reporterDir);
  }

  return [];
}

// ── Main upload logic ────────────────────────────────────────────────────────

/**
 * Upload a single seed directory (e.g. seed/cypress-ci):
 *   beginReport(total_reports_expected = shards.length)
 *   -> for each shard: issue token -> registerReport -> uploadJson
 *
 * No explicit complete call — the report group auto-finalizes once
 * `total_reports_expected` shards reach `complete`. With --incomplete,
 * one fewer shard is uploaded so the group stays `in_progress`.
 *
 * @param {string} seedDir - Path to the seed directory
 * @param {string} framework - Framework name (cypress, playwright)
 * @param {string} name - User-defined report name (e.g., "playwright-full-cloud-master")
 * @param {MockOidcProvider} oidcProvider - Mock OIDC provider for issuing per-shard tokens
 * @param {object} [options] - Upload options
 * @param {boolean} [options.incomplete] - Skip last shard so the group stays in_progress
 */
async function uploadSeedDir(seedDir, framework, name, oidcProvider, options = {}) {
  if (!fs.existsSync(seedDir) || !fs.statSync(seedDir).isDirectory()) {
    console.log(`  Warning: Directory not found: ${seedDir} (skipping)`);
    return { reports: 0, files: 0, errors: 0 };
  }

  const shards = getShardDirectories(seedDir, framework);
  if (shards.length === 0) {
    console.log(`  No shard directories found in ${seedDir} (skipping)`);
    return { reports: 0, files: 0, errors: 0 };
  }

  const context = {
    repository: SEED_CONTEXT.repository,
    commit: SEED_CONTEXT.commit,
    gh_run_id: SEED_CONTEXT.gh_run_id,
    framework: framework,
    name: name,
    branch: SEED_BRANCH_INFO.branch,
    gh_pr_number: SEED_BRANCH_INFO.pr_number ? parseInt(SEED_BRANCH_INFO.pr_number, 10) : undefined,
  };

  let totalReports = 0;
  let totalFiles = 0;
  let errors = 0;

  console.log("");
  console.log("=".repeat(60));
  console.log(
    `${framework.charAt(0).toUpperCase() + framework.slice(1)} (${shards.length} shards, run_id=${SEED_CONTEXT.gh_run_id})`,
  );
  console.log("=".repeat(60));
  console.log(`  Repository: ${context.repository}`);
  console.log(`  Branch:     ${SEED_BRANCH_INFO.branch} (${SEED_BRANCH_INFO.ref})`);
  console.log(`  Commit:     ${context.commit.slice(0, 12)}...`);

  // Step 1: Begin report (use a token for the first shard as the "begin" caller).
  // total_reports_expected is the discovered shard count — frozen server-side
  // so the auto-finalize predicate has a target. With --incomplete we still
  // declare the full count and just upload one fewer, so the group stays
  // in_progress (and the staleness reaper would eventually flip it).
  console.log("\n  [begin] Starting report...");
  const beginToken = oidcProvider.issueToken({
    check_run_id: `seed-${framework}-begin`,
  });
  const beginAuth = getBearerAuthHeaders(beginToken);
  try {
    const beginResponse = await beginReport(context, shards.length, beginAuth);
    const reportId = beginResponse.report_id;
    console.log(`  Report ID: ${reportId} (total_reports_expected=${shards.length})`);
  } catch (error) {
    console.log(
      `  Warning: beginReport failed (${error.message}) - may already exist, continuing...`,
    );
  }

  // Step 2: Process each shard with its own unique OIDC token
  const shardsToUpload = options.incomplete ? shards.length - 1 : shards.length;
  if (options.incomplete) {
    console.log(`  [incomplete] Skipping last shard (${shards[shards.length - 1].name}) and not completing report`);
  }
  for (let i = 0; i < shardsToUpload; i++) {
    const shard = shards[i];
    // Generate a numeric job ID (mimics GitHub Actions job_id)
    const ghJobId = `${Date.now()}${Math.floor(Math.random() * 10000).toString().padStart(4, "0")}`;
    const ghJobName = shard.name;

    // Issue a unique OIDC token for this shard
    const shardToken = oidcProvider.issueToken({
      check_run_id: ghJobId,
    });
    const shardAuth = getBearerAuthHeaders(shardToken);

    // Find JSON files for this shard
    const jsonFiles = findShardJsonFiles(shard.path, framework);
    if (jsonFiles.length === 0) {
      console.log(`  ${shard.name}: no JSON files, skipping`);
      continue;
    }

    // Find screenshot files for this shard (before registration so they can be declared)
    const ssDir = findScreenshotsDir(shard.path);
    const ssFiles = ssDir ? getScreenshotFiles(ssDir) : [];

    try {
      // Register report (declares JSON files and screenshots)
      // Pass environment metadata on the first upload (sets report-level metadata)
      const envMeta = (i === 0 && SEED_IMAGE) ? { server: { image: SEED_IMAGE } } : undefined;
      const registerResponse = await registerReport(context, ghJobId, ghJobName, jsonFiles, ssFiles, shardAuth, envMeta);
      const reportId = registerResponse.report_id;
      const uploadId = registerResponse.upload_id;
      const reportsInGroup = registerResponse.reports_in_group;
      const rejectedCount = (registerResponse.rejected_json_files?.length || 0)
        + (registerResponse.rejected_screenshots?.length || 0);

      // Upload JSON files
      const uploaded = await uploadJsonFiles(reportId, uploadId, jsonFiles, shardAuth);

      // Upload screenshots if present
      let ssUploaded = 0;
      if (ssFiles.length > 0) {
        ssUploaded = await uploadScreenshots(reportId, uploadId, ssFiles, shardAuth);
      }

      totalReports++;
      totalFiles += uploaded + ssUploaded;

      let statusMsg = `${uploaded}/${jsonFiles.length} JSON`;
      if (ssUploaded > 0) statusMsg += `, ${ssUploaded} screenshots`;
      if (rejectedCount > 0) statusMsg += ` (${rejectedCount} rejected)`;

      console.log(`  ${shard.name}: ${statusMsg} [token: ${ghJobId}]`);
    } catch (error) {
      console.log(`  ${shard.name}: ERROR - ${error.message}`);
      errors++;
    }
  }

  // Step 3: No explicit complete — server auto-finalizes once
  // total_reports_expected shards have uploaded. --incomplete leaves the
  // group with one fewer upload than declared so it stays in_progress.
  if (options.incomplete) {
    console.log(
      `\n  [incomplete] uploaded ${totalReports}/${shards.length} shards — group stays in_progress`,
    );
  } else {
    console.log(
      `\n  [done] uploaded ${totalReports}/${shards.length} shards — group will auto-finalize`,
    );
  }

  return { reports: totalReports, files: totalFiles, errors };
}

/**
 * Find the screenshots directory for a shard (if it exists).
 * Checks multiple locations:
 *   - results/screenshots/ (Cypress)
 *   - results/output/ (Playwright test artifacts with screenshots)
 */
function findScreenshotsDir(shardDir) {
  // Cypress: results/screenshots/
  const cypressDir = path.join(shardDir, "results", "screenshots");
  if (fs.existsSync(cypressDir) && fs.statSync(cypressDir).isDirectory()) {
    return cypressDir;
  }
  // Playwright: results/output/ (contains per-test folders with screenshots)
  const playwrightDir = path.join(shardDir, "results", "output");
  if (fs.existsSync(playwrightDir) && fs.statSync(playwrightDir).isDirectory()) {
    return playwrightDir;
  }
  return null;
}

// ── Main ─────────────────────────────────────────────────────────────────────

async function main() {
  const rawArgs = process.argv.slice(2);
  const scriptDir = __dirname;
  const projectRoot = path.dirname(scriptDir);

  // Strip named flags from positional args
  const args = [];
  let incomplete = false;
  for (let i = 0; i < rawArgs.length; i++) {
    if (rawArgs[i] === "--branch" || rawArgs[i] === "--commit" || rawArgs[i] === "--image" || rawArgs[i] === "--name" || rawArgs[i] === "--pr") {
      i++; // skip the value too
    } else if (rawArgs[i] === "--incomplete") {
      incomplete = true;
    } else {
      args.push(rawArgs[i]);
    }
  }

  console.log(`API Base: ${API_BASE}`);
  console.log(`Auth: OIDC Bearer tokens (mock provider on port ${MOCK_OIDC_PORT})`);
  console.log(`Branch: ${SEED_BRANCH_INFO.branch} (ref: ${SEED_BRANCH_INFO.ref})`);
  console.log(`Commit: ${SEED_CONTEXT.commit}`);
  console.log(`Run ID: ${SEED_CONTEXT.gh_run_id}`);
  console.log(`Batch Size: ${BATCH_SIZE}`);
  if (SEED_NAME) console.log(`Name: ${SEED_NAME}`);
  if (SEED_IMAGE) console.log(`Image: ${SEED_IMAGE}`);
  if (incomplete) console.log(`Mode: INCOMPLETE (will skip last shard and not complete)`);

  // Step 1: Start mock OIDC provider
  console.log("\n[setup] Starting mock OIDC provider...");
  const oidcProvider = new MockOidcProvider();
  await oidcProvider.start();

  // Step 2: Create OIDC policy via admin key (one-time setup)
  console.log("[setup] Creating OIDC policy...");
  await createOidcPolicy();

  let totalReports = 0;
  let totalFiles = 0;
  let totalErrors = 0;

  try {
    if (args.length >= 1) {
      // Upload specified directory: node scripts/upload-seed.js <dir> [framework]
      const dirPath = path.isAbsolute(args[0])
        ? args[0]
        : path.join(projectRoot, args[0]);

      // Detect framework from arg or directory name
      let framework = args[1] || "playwright";
      if (!args[1]) {
        const dirName = path.basename(dirPath).toLowerCase();
        if (dirName.includes("cypress")) {
          framework = "cypress";
        } else if (dirName.includes("playwright")) {
          framework = "playwright";
        }
      }

      // --name is required when uploading a specific directory
      const name = SEED_NAME;
      if (!name) {
        console.error("Error: --name is required (e.g., --name playwright-full-cloud-master)");
        process.exit(1);
      }

      const result = await uploadSeedDir(dirPath, framework, name, oidcProvider, { incomplete });
      totalReports += result.reports;
      totalFiles += result.files;
      totalErrors += result.errors;
    } else {
      // Default: upload all seed directories
      console.log("\nUploading all seed data...");

      for (const { dir, framework, name } of seedConfigs) {
        const fullPath = path.join(projectRoot, dir);
        const seedName = SEED_NAME || name;
        const result = await uploadSeedDir(fullPath, framework, seedName, oidcProvider, { incomplete });
        totalReports += result.reports;
        totalFiles += result.files;
        totalErrors += result.errors;
      }
    }
  } finally {
    // Step 3: Shut down mock JWKS server
    console.log("\n[teardown] Stopping mock OIDC provider...");
    await oidcProvider.stop();
  }

  console.log("\n" + "=".repeat(60));
  console.log("Seed Summary");
  console.log("=".repeat(60));
  console.log(`  Reports uploaded: ${totalReports}`);
  console.log(`  Files uploaded: ${totalFiles}`);
  if (totalErrors > 0) {
    console.log(`  Errors: ${totalErrors}`);
    process.exit(1);
  } else {
    console.log(`  No errors`);
  }
  console.log("");
}

main().catch((error) => {
  console.error("Fatal error:", error);
  process.exit(1);
});
