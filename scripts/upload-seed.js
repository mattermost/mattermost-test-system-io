#!/usr/bin/env node
/**
 * Upload seed data to the report server using the new job-based API.
 *
 * Usage:
 *   node scripts/upload-seed.js                     # Upload all default seed directories
 *   node scripts/upload-seed.js seed/pw-report-smoke  # Upload specific report directory
 *
 * Environment variables:
 *   API_BASE - Base URL (default: http://localhost:8080/api/v1)
 *   RRV_API_KEY - API key for authentication
 *   RRV_ADMIN_KEY - Admin key fallback (default: dev-admin-key-do-not-use-in-production)
 *   BATCH_SIZE - Number of files per upload batch (default: 50)
 *
 * Framework-specific folder structures:
 *   Cypress:    job/html/, job/screenshots/, job/json/ (folder with JSON files)
 *   Playwright: job/html/, job/screenshots/, job/json/results.json (single file)
 *   Detox:      job/html/, job/screenshots/, job/json/android-data.json (single file)
 */

const fs = require("fs");
const path = require("path");
const http = require("http");
const https = require("https");
const crypto = require("crypto");

// Configuration
const API_BASE = process.env.API_BASE || "http://localhost:8080/api/v1";
const API_KEY = process.env.RRV_API_KEY;
const ADMIN_KEY =
  process.env.RRV_ADMIN_KEY || "dev-admin-key-do-not-use-in-production";
const BATCH_SIZE = parseInt(process.env.BATCH_SIZE || "50", 10);

// Sample data for random generation
const OWNERS = [
  "acme-corp",
  "test-org",
  "my-company",
  "dev-team",
  "qa-automation",
];
const REPOS = {
  playwright: "repo-web",
  cypress: "repo-web",
  detox: "repo-mobile",
};
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

// Files/directories to exclude from HTML uploads
const EXCLUDE_PATTERNS = [".DS_Store"];
const VIDEO_EXTENSIONS = [".mp4", ".webm", ".avi", ".mov", ".mkv"];

// Allowed image extensions for screenshot uploads
const IMAGE_EXTENSIONS = [".png", ".jpg", ".jpeg", ".gif", ".webp"];

// Framework-specific JSON file/folder patterns
// Cypress: json/ folder with JSON files (mochawesome format)
// Playwright: json/results.json file
// Detox: json/android-data.json or json/ios-data.json file
const JSON_PATTERNS = {
  playwright: { type: "folder", patterns: ["json"] },
  cypress: { type: "folder", patterns: ["json"] },
  detox: { type: "folder", patterns: ["json"] },
};

// Framework-specific HTML entry file patterns
const HTML_ENTRY_PATTERNS = {
  playwright: ["index.html"],
  cypress: ["index.html", "mochawesome.html"],
  detox: ["android-report.html", "ios-report.html", "index.html"],
};

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
function generateGitHubContext(framework) {
  const owner = randomChoice(OWNERS);
  const repo = REPOS[framework] || "repo-web";
  const branch = randomChoice(BRANCHES);

  const context = {
    repo: `${owner}/${repo}`,
    branch,
    commit: generateHex(40),
    run_id: Date.now(),
    run_attempt: randomInt(1, 3),
  };

  // Add PR info for non-main branches
  if (
    branch !== "main" &&
    branch !== "develop" &&
    !branch.startsWith("release/")
  ) {
    context.pr_number = randomInt(100, 9999);
    context.pr_author = randomChoice(AUTHORS);
  }

  return context;
}

/**
 * Check if file should be excluded from HTML upload.
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
 * Find JSON files for a framework in a job directory.
 * Returns array of file info objects.
 */
function findJsonFiles(jobDir, framework) {
  const config = JSON_PATTERNS[framework] || JSON_PATTERNS.playwright;
  const files = [];

  // All frameworks now use folder pattern with json/ directory
  for (const pattern of config.patterns) {
    const folderPath = path.join(jobDir, pattern);
    if (fs.existsSync(folderPath) && fs.statSync(folderPath).isDirectory()) {
      // Recursively get all JSON files from the json/ folder
      const jsonFiles = getAllJsonFilesRecursive(folderPath, folderPath);
      files.push(...jsonFiles);
    }
  }

  return files;
}

/**
 * Recursively get all JSON files in a directory.
 */
function getAllJsonFilesRecursive(dirPath, baseDir) {
  const files = [];

  if (!fs.existsSync(dirPath)) {
    return files;
  }

  const entries = fs.readdirSync(dirPath, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      files.push(...getAllJsonFilesRecursive(fullPath, baseDir));
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
  const files = [];

  if (!fs.existsSync(screenshotsDir)) {
    return files;
  }

  const entries = fs.readdirSync(screenshotsDir, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(screenshotsDir, entry.name);
    if (entry.isDirectory()) {
      // Recursively get files from subdirectories (test-name folders)
      const subFiles = getAllFilesRecursive(fullPath, screenshotsDir);
      for (const file of subFiles) {
        if (isImageFile(file.relativePath) && !shouldExclude(file.relativePath)) {
          files.push(file);
        }
      }
    } else if (entry.isFile() && isImageFile(entry.name) && !shouldExclude(entry.name)) {
      // Handle root-level screenshots
      const stats = fs.statSync(fullPath);
      files.push({
        fullPath,
        relativePath: entry.name,
        size: stats.size,
        contentType: getMimeType(entry.name),
      });
    }
  }

  return files;
}

/**
 * Recursively get all files in a directory with relative paths.
 */
function getAllFilesRecursive(dirPath, baseDir) {
  const files = [];

  if (!fs.existsSync(dirPath)) {
    return files;
  }

  const entries = fs.readdirSync(dirPath, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      files.push(...getAllFilesRecursive(fullPath, baseDir));
    } else if (entry.isFile()) {
      const relativePath = path.relative(baseDir, fullPath);
      const stats = fs.statSync(fullPath);
      files.push({
        fullPath,
        relativePath,
        size: stats.size,
        contentType: getMimeType(entry.name),
      });
    }
  }

  return files;
}

/**
 * Get job directories within a report directory.
 */
function getJobDirectories(reportDir) {
  const entries = fs.readdirSync(reportDir, { withFileTypes: true });
  const jobDirs = [];

  for (const entry of entries) {
    if (entry.isDirectory() && !entry.name.startsWith(".")) {
      const jobPath = path.join(reportDir, entry.name);
      const htmlDir = path.join(jobPath, "html");
      const screenshotsDir = path.join(jobPath, "screenshots");

      // Check if this is a valid job directory (has html subdirectory)
      if (fs.existsSync(htmlDir)) {
        jobDirs.push({
          name: entry.name,
          path: jobPath,
          htmlDir,
          screenshotsDir: fs.existsSync(screenshotsDir) ? screenshotsDir : null,
        });
      }
    }
  }

  return jobDirs;
}

/**
 * Build auth headers based on available keys.
 */
function getAuthHeaders() {
  const headers = {};
  if (API_KEY) {
    headers["X-API-Key"] = API_KEY;
  } else {
    headers["X-Admin-Key"] = ADMIN_KEY;
  }
  return headers;
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
 */
function uploadFilesMultipart(url, files, baseDir) {
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
      ...getAuthHeaders(),
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

/**
 * Step 1: Register a new report.
 */
async function registerReport(framework, expectedJobs, githubContext) {
  const url = `${API_BASE}/reports`;
  const body = JSON.stringify({
    framework,
    expected_jobs: expectedJobs,
    github_metadata: githubContext,
  });

  const headers = {
    ...getAuthHeaders(),
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode !== 201) {
    throw new Error(
      `Failed to register report (${response.statusCode}): ${response.body}`,
    );
  }

  return JSON.parse(response.body);
}

/**
 * Step 2: Initialize a job.
 */
async function initJob(reportId, jobName) {
  const url = `${API_BASE}/reports/${reportId}/jobs/init`;
  const body = JSON.stringify({
    github_metadata: {
      job_name: jobName,
    },
  });

  const headers = {
    ...getAuthHeaders(),
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode !== 200) {
    throw new Error(
      `Failed to init job (${response.statusCode}): ${response.body}`,
    );
  }

  return JSON.parse(response.body);
}

/**
 * Step 3a: Initialize HTML files (request-then-transfer pattern).
 */
async function initHtml(reportId, jobId, files) {
  const url = `${API_BASE}/reports/${reportId}/jobs/${jobId}/html/init`;
  const body = JSON.stringify({
    files: files.map((f) => ({
      path: f.relativePath,
      size: f.size,
      content_type: f.contentType,
    })),
  });

  const headers = {
    ...getAuthHeaders(),
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode !== 200) {
    throw new Error(
      `Failed to init HTML (${response.statusCode}): ${response.body}`,
    );
  }

  return JSON.parse(response.body);
}

/**
 * Step 3b: Upload HTML files in batches.
 */
async function uploadHtmlFiles(reportId, jobId, files, htmlDir) {
  const url = `${API_BASE}/reports/${reportId}/jobs/${jobId}/html`;
  let totalUploaded = 0;

  // Upload in batches
  for (let i = 0; i < files.length; i += BATCH_SIZE) {
    const batch = files.slice(i, i + BATCH_SIZE);
    const batchNum = Math.floor(i / BATCH_SIZE) + 1;
    const totalBatches = Math.ceil(files.length / BATCH_SIZE);

    console.log(
      `    Batch ${batchNum}/${totalBatches}: uploading ${batch.length} files...`,
    );

    const response = await uploadFilesMultipart(url, batch, htmlDir);

    if (response.statusCode !== 200) {
      throw new Error(
        `Failed to upload HTML files (${response.statusCode}): ${response.body}`,
      );
    }

    const result = JSON.parse(response.body);
    totalUploaded += result.files_uploaded;
    console.log(
      `    Progress: ${result.total_uploaded}/${result.total_expected} files`,
    );
  }

  return totalUploaded;
}

/**
 * Step 4a: Initialize screenshots (request-then-transfer pattern).
 */
async function initScreenshots(reportId, jobId, files) {
  const url = `${API_BASE}/reports/${reportId}/jobs/${jobId}/screenshots/init`;
  const body = JSON.stringify({
    files: files.map((f) => ({
      path: f.relativePath,
      size: f.size,
      content_type: f.contentType,
    })),
  });

  const headers = {
    ...getAuthHeaders(),
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode !== 200) {
    throw new Error(
      `Failed to init screenshots (${response.statusCode}): ${response.body}`,
    );
  }

  return JSON.parse(response.body);
}

/**
 * Step 4b: Upload screenshot files.
 */
async function uploadScreenshots(reportId, jobId, files) {
  const url = `${API_BASE}/reports/${reportId}/jobs/${jobId}/screenshots`;
  let totalUploaded = 0;

  // Upload in batches
  for (let i = 0; i < files.length; i += BATCH_SIZE) {
    const batch = files.slice(i, i + BATCH_SIZE);
    const batchNum = Math.floor(i / BATCH_SIZE) + 1;
    const totalBatches = Math.ceil(files.length / BATCH_SIZE);

    console.log(
      `    Batch ${batchNum}/${totalBatches}: uploading ${batch.length} screenshots...`,
    );

    const response = await uploadFilesMultipart(url, batch, null);

    if (response.statusCode !== 200) {
      throw new Error(
        `Failed to upload screenshots (${response.statusCode}): ${response.body}`,
      );
    }

    const result = JSON.parse(response.body);
    totalUploaded += result.files_uploaded;
    console.log(
      `    Progress: ${result.total_uploaded}/${result.total_expected} screenshots`,
    );
  }

  return totalUploaded;
}

/**
 * Step 5a: Initialize JSON files (request-then-transfer pattern).
 */
async function initJson(reportId, jobId, files) {
  const url = `${API_BASE}/reports/${reportId}/jobs/${jobId}/json/init`;
  const body = JSON.stringify({
    files: files.map((f) => ({
      path: f.relativePath,
      size: f.size,
      content_type: f.contentType,
    })),
  });

  const headers = {
    ...getAuthHeaders(),
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(body),
  };

  const response = await makeRequest(url, { method: "POST", headers }, body);

  if (response.statusCode !== 200) {
    throw new Error(
      `Failed to init JSON (${response.statusCode}): ${response.body}`,
    );
  }

  return JSON.parse(response.body);
}

/**
 * Step 5b: Upload JSON files.
 */
async function uploadJson(reportId, jobId, files) {
  const url = `${API_BASE}/reports/${reportId}/jobs/${jobId}/json`;
  let totalUploaded = 0;

  // Upload in batches
  for (let i = 0; i < files.length; i += BATCH_SIZE) {
    const batch = files.slice(i, i + BATCH_SIZE);
    const batchNum = Math.floor(i / BATCH_SIZE) + 1;
    const totalBatches = Math.ceil(files.length / BATCH_SIZE);

    if (totalBatches > 1) {
      console.log(
        `    Batch ${batchNum}/${totalBatches}: uploading ${batch.length} JSON files...`,
      );
    }

    const response = await uploadFilesMultipart(url, batch, null);

    if (response.statusCode !== 200) {
      throw new Error(
        `Failed to upload JSON files (${response.statusCode}): ${response.body}`,
      );
    }

    const result = JSON.parse(response.body);
    totalUploaded += result.files_uploaded;

    if (result.extraction_triggered) {
      console.log(`    Extraction triggered`);
    }
  }

  return totalUploaded;
}

/**
 * Upload a single report directory (containing multiple job subdirectories).
 */
async function uploadReport(reportDir, framework = "playwright") {
  if (!fs.existsSync(reportDir) || !fs.statSync(reportDir).isDirectory()) {
    console.log(`Warning: Directory not found: ${reportDir} (skipping)`);
    return false;
  }

  const reportName = path.basename(reportDir);

  console.log("");
  console.log("=".repeat(60));
  console.log(`Report: ${reportName} (${framework})`);
  console.log("=".repeat(60));

  // Get job directories
  const jobDirs = getJobDirectories(reportDir);
  if (jobDirs.length === 0) {
    console.log("  No job directories found (skipping)");
    return false;
  }

  console.log(`  Found ${jobDirs.length} job(s)`);

  // Generate GitHub context
  const githubContext = generateGitHubContext(framework);
  console.log(`  Repository: ${githubContext.repo}`);
  console.log(`  Branch: ${githubContext.branch}`);

  try {
    // Step 1: Register report
    console.log("\n[1/5] Registering report...");
    const reportResponse = await registerReport(
      framework,
      jobDirs.length,
      githubContext,
    );
    const reportId = reportResponse.report_id;
    console.log(`  Report ID: ${reportId}`);
    console.log(`  Status: ${reportResponse.status}`);

    // Process each job
    for (let i = 0; i < jobDirs.length; i++) {
      const job = jobDirs[i];
      console.log(`\n--- Job ${i + 1}/${jobDirs.length}: ${job.name} ---`);

      // Step 2: Initialize job
      console.log("[2/5] Initializing job...");
      const initResponse = await initJob(reportId, job.name);
      const jobId = initResponse.job_id;
      console.log(`  Job ID: ${jobId}`);

      // Step 3: Upload HTML files (optional)
      const htmlFiles = getAllFiles(job.htmlDir);
      if (htmlFiles.length > 0) {
        console.log(`[3/5] Uploading ${htmlFiles.length} HTML files...`);

        // Step 3a: Initialize HTML
        console.log("  Initializing HTML files...");
        const initHtmlResponse = await initHtml(reportId, jobId, htmlFiles);
        console.log(`  Accepted: ${initHtmlResponse.accepted_files.length} files`);
        if (initHtmlResponse.rejected_files && initHtmlResponse.rejected_files.length > 0) {
          console.log(`  Rejected: ${initHtmlResponse.rejected_files.length} files`);
          for (const rejected of initHtmlResponse.rejected_files.slice(0, 3)) {
            console.log(`    - ${rejected.path}: ${rejected.reason}`);
          }
        }

        // Step 3b: Upload HTML files
        const uploadedHtml = await uploadHtmlFiles(reportId, jobId, htmlFiles, job.htmlDir);
        console.log(`  Uploaded ${uploadedHtml} HTML files`);
      } else {
        console.log("[3/5] No HTML files found (skipping)");
      }

      // Step 4: Upload screenshots (optional)
      if (job.screenshotsDir) {
        const screenshotFiles = getScreenshotFiles(job.screenshotsDir);
        if (screenshotFiles.length > 0) {
          console.log(`[4/5] Uploading ${screenshotFiles.length} screenshots...`);

          // Step 4a: Initialize screenshots
          console.log("  Initializing screenshots...");
          const initSsResponse = await initScreenshots(reportId, jobId, screenshotFiles);
          console.log(`  Accepted: ${initSsResponse.accepted_files.length} screenshots`);
          if (initSsResponse.rejected_files && initSsResponse.rejected_files.length > 0) {
            console.log(`  Rejected: ${initSsResponse.rejected_files.length} screenshots`);
            for (const rejected of initSsResponse.rejected_files.slice(0, 3)) {
              console.log(`    - ${rejected.path}: ${rejected.reason}`);
            }
          }

          // Step 4b: Upload screenshot files
          const uploadedSs = await uploadScreenshots(reportId, jobId, screenshotFiles);
          console.log(`  Uploaded ${uploadedSs} screenshots`);
        } else {
          console.log("[4/5] No screenshots found (skipping)");
        }
      } else {
        console.log("[4/5] No screenshots directory (skipping)");
      }

      // Step 5: Upload JSON files (required)
      const jsonFiles = findJsonFiles(job.path, framework);
      if (jsonFiles.length > 0) {
        console.log(`[5/5] Uploading ${jsonFiles.length} JSON file(s)...`);

        // Step 5a: Initialize JSON
        console.log("  Initializing JSON files...");
        const initJsonResponse = await initJson(reportId, jobId, jsonFiles);
        console.log(`  Accepted: ${initJsonResponse.accepted_files.length} files`);
        if (initJsonResponse.rejected_files && initJsonResponse.rejected_files.length > 0) {
          console.log(`  Rejected: ${initJsonResponse.rejected_files.length} files`);
          for (const rejected of initJsonResponse.rejected_files.slice(0, 3)) {
            console.log(`    - ${rejected.path}: ${rejected.reason}`);
          }
        }

        // Step 5b: Upload JSON files
        const uploadedJson = await uploadJson(reportId, jobId, jsonFiles);
        console.log(`  Uploaded ${uploadedJson} JSON file(s)`);
      } else {
        console.log("[5/5] WARNING: No JSON files found!");
        console.log("  JSON files are required for test data extraction.");
        console.log(`  Expected: json/ folder with .json files`);
      }
    }

    console.log(`\nReport ${reportId} uploaded successfully!`);
    return true;
  } catch (error) {
    console.log(`\nError: ${error.message}`);
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

  console.log(`API Base: ${API_BASE}`);
  console.log(`Auth: ${API_KEY ? "API Key" : "Admin Key"}`);
  console.log(`Batch Size: ${BATCH_SIZE}`);

  if (args.length > 0) {
    // Upload specified directories
    for (const dirPath of args) {
      const fullPath = path.isAbsolute(dirPath)
        ? dirPath
        : path.join(projectRoot, dirPath);

      // Try to detect framework from directory name
      let framework = "playwright";
      const dirName = path.basename(fullPath).toLowerCase();
      if (dirName.includes("cy") || dirName.includes("cypress")) {
        framework = "cypress";
      } else if (dirName.includes("detox")) {
        framework = "detox";
      }

      await uploadReport(fullPath, framework);
    }
  } else {
    // Default: upload all seed directories
    console.log("\nUploading all seed data...\n");

    const seedConfigs = [
      { dir: "seed/playwright-report", framework: "playwright" },
      { dir: "seed/cypress-report", framework: "cypress" },
      { dir: "seed/cypress-report-with-empty", framework: "cypress" },
      { dir: "seed/detox-android-report", framework: "detox" },
    ];

    for (const { dir, framework } of seedConfigs) {
      await uploadReport(path.join(projectRoot, dir), framework);
    }
  }

  console.log("\n" + "=".repeat(60));
  console.log("Done!");
}

main().catch((error) => {
  console.error("Fatal error:", error);
  process.exit(1);
});
