#!/usr/bin/env node
/**
 * Upload seed data to the report server using Node.js stdlib.
 *
 * Usage:
 *   node scripts/upload-seed.js                     # Upload all default seed directories
 *   node scripts/upload-seed.js seed/my-report      # Upload specific directory
 */

const fs = require("fs");
const path = require("path");
const http = require("http");
const https = require("https");
const crypto = require("crypto");

// Configuration
const API_BASE = process.env.API_BASE || "http://localhost:8080/api/v1";
// Use RRV_API_KEY for database-backed API keys, or fall back to admin key for development
const API_KEY = process.env.RRV_API_KEY;
const ADMIN_KEY =
  process.env.RRV_ADMIN_KEY || "dev-admin-key-do-not-use-in-production";

// Framework-specific upload URLs (two-phase API)
const UPLOAD_REQUEST_URLS = {
  playwright: `${API_BASE}/reports/upload/playwright/request`,
  cypress: `${API_BASE}/reports/upload/cypress/request`,
  detox: `${API_BASE}/reports/upload/detox/request`,
};
const UPLOAD_FILES_URL = `${API_BASE}/reports/upload`;

// Sample data for random generation
const OWNERS = [
  "acme-corp",
  "test-org",
  "my-company",
  "dev-team",
  "qa-automation",
];
const REPOS = [
  "web-app",
  "api-server",
  "e2e-tests",
  "frontend",
  "backend",
  "mobile-app",
];
const BRANCHES = [
  "main",
  "develop",
  "feature/auth",
  "feature/dashboard",
  "fix/login-bug",
  "release/v2.0",
  "hotfix/security",
];
const AUTHORS = [
  "john-doe",
  "jane-smith",
  "bob-wilson",
  "alice-johnson",
  "dev-bot",
];

// Files/directories to exclude
const EXCLUDE_PATTERNS = [".DS_Store", "json/"];
const VIDEO_EXTENSIONS = [".mp4", ".webm", ".avi", ".mov", ".mkv"];

/**
 * Generate random hex string.
 */
function generateHex(length) {
  return crypto
    .randomBytes(Math.ceil(length / 2))
    .toString("hex")
    .slice(0, length);
}

/**
 * Pick random element from array.
 */
function randomChoice(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

/**
 * Generate random integer in range [min, max].
 */
function randomInt(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

/**
 * Generate random GitHub context.
 */
function generateGitHubContext() {
  const owner = randomChoice(OWNERS);
  const repo = randomChoice(REPOS);
  const branch = randomChoice(BRANCHES);

  const context = {
    repository: `${owner}/${repo}`,
    branch,
    commit_sha: generateHex(40),
    run_id: Date.now(), // Milliseconds resolution for uniqueness
    run_attempt: randomInt(1, 3),
  };

  // Add PR info for non-main branches
  if (
    branch !== "main" &&
    branch !== "develop" &&
    !branch.startsWith("release/")
  ) {
    context.pr_number = randomInt(100, 9999); // Random PR number
    context.pr_author = randomChoice(AUTHORS);
  }

  return context;
}

/**
 * Check if file should be excluded.
 */
function shouldExclude(filepath) {
  // Check exclude patterns
  for (const pattern of EXCLUDE_PATTERNS) {
    if (filepath.includes(pattern)) {
      return true;
    }
  }

  // Check video extensions
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

  const entries = fs.readdirSync(dirPath, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      files.push(...getAllFiles(fullPath, baseDir));
    } else if (entry.isFile()) {
      const relativePath = path.relative(baseDir, fullPath);
      if (!shouldExclude(relativePath)) {
        files.push({ fullPath, relativePath });
      }
    }
  }

  return files;
}

/**
 * Build multipart form data.
 */
class MultipartFormData {
  constructor() {
    this.boundary = `----WebKitFormBoundary${crypto.randomUUID().replace(/-/g, "")}`;
    this.parts = [];
  }

  addField(name, value) {
    this.parts.push(
      `--${this.boundary}\r\n` +
        `Content-Disposition: form-data; name="${name}"\r\n\r\n` +
        `${value}\r\n`,
    );
  }

  addFile(name, filename, content, contentType = "application/octet-stream") {
    this.parts.push(
      `--${this.boundary}\r\n` +
        `Content-Disposition: form-data; name="${name}"; filename="${filename}"\r\n` +
        `Content-Type: ${contentType}\r\n\r\n`,
    );
    this.parts.push(content);
    this.parts.push("\r\n");
  }

  getContentType() {
    return `multipart/form-data; boundary=${this.boundary}`;
  }

  getBody() {
    const buffers = this.parts.map((part) =>
      typeof part === "string" ? Buffer.from(part, "utf-8") : part,
    );
    buffers.push(Buffer.from(`--${this.boundary}--\r\n`, "utf-8"));
    return Buffer.concat(buffers);
  }
}

/**
 * Make HTTP request.
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
        timeout: 300000, // 5 minutes
      },
      (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => {
          resolve({ statusCode: res.statusCode, body: data });
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
 * Upload a single directory using two-phase API.
 * @param {string} seedDir - Path to the seed directory
 * @param {string} [repoName] - Optional repository name override (e.g., "repo-mobile", "repo-web")
 * @param {string} [framework] - Framework type: "playwright", "cypress", or "detox"
 * @param {string} [platform] - Platform for Detox: "ios" or "android"
 */
async function uploadDirectory(seedDir, repoName, framework = "playwright", platform) {
  if (!fs.existsSync(seedDir) || !fs.statSync(seedDir).isDirectory()) {
    console.log(`Warning: Directory not found: ${seedDir} (skipping)`);
    return false;
  }

  console.log("");
  console.log("=".repeat(50));
  const platformStr = platform ? ` - ${platform}` : "";
  console.log(`Uploading: ${path.basename(seedDir)} (${framework}${platformStr})`);
  console.log("=".repeat(50));

  // Generate GitHub context with optional repo override
  const githubContext = generateGitHubContext();
  if (repoName) {
    githubContext.repository = `test-org/${repoName}`;
  }
  console.log("GitHub Context:");
  console.log(`  repository: ${githubContext.repository}`);
  console.log(`  branch: ${githubContext.branch}`);
  console.log("  ...");

  // Collect all files (server handles size limits and screenshot deprioritization)
  const allFiles = getAllFiles(seedDir);
  const filenames = allFiles.map((f) => f.relativePath);
  console.log(`Found ${allFiles.length} files`);

  // ===== Phase 1: Request upload =====
  console.log("\n[Phase 1] Requesting upload...");
  const requestUrl = UPLOAD_REQUEST_URLS[framework] || UPLOAD_REQUEST_URLS.playwright;
  const requestBody = {
    framework_version: "1.0.0",
    github_context: githubContext,
    filenames,
  };

  // Add platform for Detox
  if (framework === "detox" && platform) {
    requestBody.platform = platform;
  }

  // Use X-API-Key if RRV_API_KEY is set, otherwise use X-Admin-Key for development
  const requestHeaders = {
    "Content-Type": "application/json",
  };
  if (API_KEY) {
    requestHeaders["X-API-Key"] = API_KEY;
  } else {
    requestHeaders["X-Admin-Key"] = ADMIN_KEY;
  }

  const phase1Body = JSON.stringify(requestBody);
  requestHeaders["Content-Length"] = Buffer.byteLength(phase1Body);

  try {
    const phase1Response = await makeRequest(
      requestUrl,
      { method: "POST", headers: requestHeaders },
      phase1Body,
    );

    if (phase1Response.statusCode !== 201) {
      console.log(`Phase 1 failed (${phase1Response.statusCode}):`);
      console.log(phase1Response.body);
      return false;
    }

    const phase1Data = JSON.parse(phase1Response.body);
    const reportId = phase1Data.report_id;
    const maxUploadSize = phase1Data.max_upload_size;
    console.log(`  Report ID: ${reportId}`);
    console.log(`  Max upload size: ${(maxUploadSize / 1024 / 1024).toFixed(1)}MB`);
    console.log(`  Files accepted: ${phase1Data.files_accepted.length}`);
    console.log(`  Files rejected: ${phase1Data.files_rejected.length}`);

    if (phase1Data.files_rejected.length > 0) {
      console.log("  Rejected files:");
      for (const rej of phase1Data.files_rejected) {
        console.log(`    - ${rej.file}: ${rej.reason}`);
      }
    }

    // ===== Phase 2: Upload files =====
    console.log("\n[Phase 2] Uploading files...");
    const uploadUrl = `${UPLOAD_FILES_URL}/${reportId}/files`;

    // Build multipart form data with only accepted files
    const form = new MultipartFormData();
    const acceptedSet = new Set(phase1Data.files_accepted);

    for (const { fullPath, relativePath } of allFiles) {
      if (acceptedSet.has(relativePath)) {
        const content = fs.readFileSync(fullPath);
        const mimeType = getMimeType(relativePath);
        form.addFile("files", relativePath, content, mimeType);
      }
    }

    const body = form.getBody();
    const headers = {
      "Content-Type": form.getContentType(),
      "Content-Length": body.length,
    };
    if (API_KEY) {
      headers["X-API-Key"] = API_KEY;
    } else {
      headers["X-Admin-Key"] = ADMIN_KEY;
    }

    const phase2Response = await makeRequest(
      uploadUrl,
      { method: "POST", headers },
      body,
    );

    console.log(`\nResponse (${phase2Response.statusCode}):`);
    try {
      const responseData = JSON.parse(phase2Response.body);
      console.log(JSON.stringify(responseData, null, 2));
    } catch {
      console.log(phase2Response.body);
    }

    return phase2Response.statusCode === 200;
  } catch (error) {
    console.log(`Error: ${error.message}`);
    return false;
  }
}

/**
 * Main function.
 */
async function main() {
  const args = process.argv.slice(2);
  const scriptDir = __dirname;
  const projectRoot = path.dirname(scriptDir);

  if (args.length > 0) {
    // Upload specified directories
    for (const dirPath of args) {
      const fullPath = path.isAbsolute(dirPath)
        ? dirPath
        : path.join(projectRoot, dirPath);
      await uploadDirectory(fullPath);
    }
  } else {
    // Default: upload all seed directories
    console.log(`API Base: ${API_BASE}`);
    console.log("Uploading all seed data...");

    // Playwright, Cypress and Detox reports with their repository names and frameworks
    // - Playwright/Cypress: repo-web
    // - Detox: repo-mobile (platform: android - using detox-android-many and detox-android-one)
    const seedConfigs = [
      {
        dir: "seed/pw-report-smoke",
        repo: "repo-web",
        framework: "playwright",
      },
      {
        dir: "seed/pw-report-with-failed",
        repo: "repo-web",
        framework: "playwright",
      },
      {
        dir: "seed/pw-report-with-skipped",
        repo: "repo-web",
        framework: "playwright",
      },
      {
        dir: "seed/cy-mochawesome-report-1",
        repo: "repo-web",
        framework: "cypress",
      },
      {
        dir: "seed/cy-mochawesome-report-2",
        repo: "repo-web",
        framework: "cypress",
      },
      { dir: "seed/detox-android-many", repo: "repo-mobile", framework: "detox", platform: "android" },
      { dir: "seed/detox-android-one", repo: "repo-mobile", framework: "detox", platform: "android" },
    ];

    for (const { dir, repo, framework, platform } of seedConfigs) {
      await uploadDirectory(path.join(projectRoot, dir), repo, framework, platform);
    }
  }

  console.log("");
  console.log("Done!");
}

main().catch((error) => {
  console.error("Fatal error:", error);
  process.exit(1);
});
