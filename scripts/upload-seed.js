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
const API_URL = process.env.API_URL || "http://localhost:8080/api/v1/reports";
const API_KEY =
  process.env.RRV_API_KEY || "dev-api-key-do-not-use-in-production";

// Sample data for random generation
const OWNERS = ["acme-corp", "test-org", "my-company", "dev-team", "qa-automation"];
const REPOS = ["web-app", "api-server", "e2e-tests", "frontend", "backend", "mobile-app"];
const BRANCHES = [
  "main",
  "develop",
  "feature/auth",
  "feature/dashboard",
  "fix/login-bug",
  "release/v2.0",
  "hotfix/security",
];
const AUTHORS = ["john-doe", "jane-smith", "bob-wilson", "alice-johnson", "dev-bot"];

// Files/directories to exclude
const EXCLUDE_PATTERNS = [".DS_Store", "json/"];
const VIDEO_EXTENSIONS = [".mp4", ".webm", ".avi", ".mov", ".mkv"];

/**
 * Generate random hex string.
 */
function generateHex(length) {
  return crypto.randomBytes(Math.ceil(length / 2)).toString("hex").slice(0, length);
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
    run_id: Math.floor(Date.now() / 1000),
    run_attempt: randomInt(1, 3),
  };

  // Add PR info for non-main branches
  if (branch !== "main" && branch !== "develop" && !branch.startsWith("release/")) {
    context.pr_number = Math.floor(Date.now() / 1000);
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
      `${value}\r\n`
    );
  }

  addFile(name, filename, content, contentType = "application/octet-stream") {
    this.parts.push(
      `--${this.boundary}\r\n` +
      `Content-Disposition: form-data; name="${name}"; filename="${filename}"\r\n` +
      `Content-Type: ${contentType}\r\n\r\n`
    );
    this.parts.push(content);
    this.parts.push("\r\n");
  }

  getContentType() {
    return `multipart/form-data; boundary=${this.boundary}`;
  }

  getBody() {
    const buffers = this.parts.map((part) =>
      typeof part === "string" ? Buffer.from(part, "utf-8") : part
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
      }
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
 * Upload a single directory.
 */
async function uploadDirectory(seedDir) {
  if (!fs.existsSync(seedDir) || !fs.statSync(seedDir).isDirectory()) {
    console.log(`Warning: Directory not found: ${seedDir} (skipping)`);
    return false;
  }

  console.log("");
  console.log("=".repeat(41));
  console.log(`Uploading: ${path.basename(seedDir)}`);
  console.log("=".repeat(41));

  // Generate GitHub context
  const githubContext = generateGitHubContext();
  console.log("GitHub Context:");
  console.log(`  repository: ${githubContext.repository}`);
  console.log(`  branch: ${githubContext.branch}`);
  console.log("  ...");

  // Collect all files
  const filesToUpload = getAllFiles(seedDir);
  console.log(`Found ${filesToUpload.length} files`);

  // Build multipart form data
  const form = new MultipartFormData();
  form.addField("github_context", JSON.stringify(githubContext));

  for (const { fullPath, relativePath } of filesToUpload) {
    const content = fs.readFileSync(fullPath);
    const mimeType = getMimeType(relativePath);
    form.addFile("files", relativePath, content, mimeType);
  }

  // Make request
  const body = form.getBody();
  const headers = {
    "X-API-Key": API_KEY,
    "Content-Type": form.getContentType(),
    "Content-Length": body.length,
  };

  try {
    const response = await makeRequest(API_URL, { method: "POST", headers }, body);

    console.log(`Response (${response.statusCode}):`);
    try {
      console.log(JSON.stringify(JSON.parse(response.body), null, 2));
    } catch {
      console.log(response.body);
    }

    return response.statusCode === 201;
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
    console.log(`API URL: ${API_URL}`);
    console.log("Uploading all seed data...");

    const seedDirs = [
      "seed/pw-report-smoke",
      "seed/pw-report-with-failed",
      "seed/pw-report-with-skipped",
      "seed/cy-mochawesome-report-1",
      "seed/cy-mochawesome-report-2",
    ];

    for (const seedDir of seedDirs) {
      await uploadDirectory(path.join(projectRoot, seedDir));
    }
  }

  console.log("");
  console.log("Done!");
}

main().catch((error) => {
  console.error("Fatal error:", error);
  process.exit(1);
});
