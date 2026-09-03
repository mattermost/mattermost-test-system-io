// Minimal JSON HTTP client shared by worker.js and replay.js. JSON-only —
// multipart upload lives in reports_client.js instead.

'use strict';

const http = require('http');

function request(apiBase, apiKey, method, urlPath, body) {
  return new Promise((resolve, reject) => {
    const url = new URL(urlPath, apiBase);
    const data = body == null ? null : Buffer.from(JSON.stringify(body));
    const req = http.request(
      {
        method,
        hostname: url.hostname,
        port: url.port,
        path: url.pathname + url.search,
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': apiKey,
          ...(data ? { 'Content-Length': data.length } : {}),
        },
      },
      (res) => {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          let parsed = null;
          if (text.length) {
            try {
              parsed = JSON.parse(text);
            } catch {
              parsed = text;
            }
          }
          resolve({ status: res.statusCode, body: parsed });
        });
      },
    );
    req.on('error', reject);
    if (data) req.write(data);
    req.end();
  });
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

module.exports = { request, sleep };
