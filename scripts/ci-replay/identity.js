'use strict';

const crypto = require('crypto');

const MATTERMOST_GROUPS = new Set([
  'cypress-full',
  'cypress-full-fips',
  'playwright-full',
  'playwright-full-fips',
]);

const MOBILE_GROUPS = new Set([
  'detox-ios',
  'detox-android',
  'detox-ipad',
  'maestro-ios',
  'maestro-android',
]);

/** Random 40-char commit SHA. */
function randomCommitSha() {
  return crypto.randomBytes(20).toString('hex');
}

function isMattermostGroup(group) {
  return MATTERMOST_GROUPS.has(group);
}

function isMobileGroup(group) {
  return MOBILE_GROUPS.has(group);
}

/** Replay commit for a test group. */
function commitShaForGroup(group, batchCommits) {
  if (process.env.TSIO_COMMIT_SHA) {
    return process.env.TSIO_COMMIT_SHA.toLowerCase();
  }
  if (batchCommits) {
    if (isMattermostGroup(group)) return batchCommits.mattermost;
    if (isMobileGroup(group)) return batchCommits.mobile;
  }
  return randomCommitSha();
}

/** gh_run_<n> wire format. */
function ghRunId(nowMs = Date.now()) {
  const raw = process.env.TSIO_GH_RUN_ID || String(nowMs);
  return raw.startsWith('gh_run_') ? raw : `gh_run_${raw}`;
}

function isReplayGhRunId(ghRunId) {
  return typeof ghRunId === 'string' && ghRunId.startsWith('gh_run_');
}

module.exports = {
  MATTERMOST_GROUPS,
  MOBILE_GROUPS,
  randomCommitSha,
  commitShaForGroup,
  ghRunId,
  isMattermostGroup,
  isMobileGroup,
  isReplayGhRunId,
};
