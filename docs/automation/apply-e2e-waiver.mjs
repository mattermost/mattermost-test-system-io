#!/usr/bin/env node
// Run only from the reviewed, pinned TSIO revision in a clean environment.
// This process executes no repository code or commands and sends its token only
// to api.github.com. The decision remains an agent attestation, not a causal proof.
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

export const VERSION = 'e2e-waiver-v1';
export const BASE = 'https://staging-test-io.test.mattermost.com/api/v1';
const REPO = 'mattermost/mattermost';
const CONTEXTS = new Set(['playwright', 'cypress'].flatMap((f) =>
    ['enterprise', 'fips'].map((edition) => `e2e-test/${f}-full/${edition}`)));
const fail = (message) => { throw new Error(message); };
const requireThat = (condition, message) => { if (!condition) fail(message); };
const sha = (value) => /^[a-f0-9]{40}$/.test(value || '');
const positive = (value) => /^[1-9][0-9]*$/.test(String(value));
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const canonical = (value) => Array.isArray(value) ? value.map(canonical) :
    value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map((k) => [k, canonical(value[k])])) : value;
const digest = (value) => createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex');
const query = (object) => new URLSearchParams(object).toString();

function clusterKeys(pack) {
    requireThat(pack.complete === true && pack.truncated === false, 'Evidence is incomplete or truncated');
    requireThat(pack.group.status === 'completed' && pack.group.total_reports_expected > 0 &&
        pack.group.reports_registered === pack.group.total_reports_expected &&
        pack.group.reports_complete === pack.group.total_reports_expected, 'Report coverage is incomplete');
    requireThat(Array.isArray(pack.clusters) && pack.clusters.length === pack.cluster_count, 'Cluster count mismatch');
    const members = pack.clusters.flatMap((cluster) => {
        requireThat(cluster.member_count === cluster.members.length, 'A cluster hides members');
        return cluster.members;
    });
    requireThat(members.length === pack.failure_count, 'Failure count mismatch');
    const keys = members.map((member) => member.stable_key);
    requireThat(keys.every((key) => typeof key === 'string' && key.length > 0) &&
        new Set(keys).size === keys.length, 'Missing or duplicate failure identities');
    return keys;
}

function validateReproduction(test, decision, environment) {
    requireThat(test.verdict === 'BASELINE_REPRODUCED', 'Every failure requires paired reproduction');
    requireThat(test.diff_suspect === false && typeof test.diff_reason === 'string' && test.diff_reason.length >= 20,
        'A related or unreviewed diff vetoes the waiver');
    const r = test.reproduction;
    requireThat(r && r.pr_sha === decision.head_sha && r.base_sha === decision.merge_base_sha,
        'Reproduction must compare the assessed head with its merge base');
    requireThat(r.environment_matches_ci === true && r.credentials_isolated === true,
        'Reproduction environment or credential isolation was not established');
    requireThat(r.pr_image === environment.server_image_digest && /@sha256:[a-f0-9]{64}$/.test(r.pr_image || '') &&
        /@sha256:[a-f0-9]{64}$/.test(r.base_image || ''), 'Reproduction requires pullable immutable images');
    requireThat(r.base_image_commit === decision.merge_base_sha, 'Baseline image provenance must identify the merge base');
    requireThat(r.same_failure_mode === true && r.unexplained_failures === 0,
        'A new or unexplained failure vetoes the waiver');
    for (const side of ['pr', 'base']) {
        const attempts = r[`${side}_runs`];
        requireThat(Array.isArray(attempts) && attempts.length >= 3 && attempts.every((run) =>
            ['passed', 'failed'].includes(run.outcome) && typeof run.log === 'string' && run.log.length >= 20 &&
            run.retries === 0), 'Each side needs three independent runs with retries disabled and retained logs');
        requireThat(attempts.some((run) => run.outcome === 'failed'), 'Matching failures must actually reproduce on both commits');
    }
}

function validateOrchestration(run, group, pack) {
    for (const key of ['repository', 'commit_sha', 'gh_run_id', 'gh_run_attempt', 'name']) {
        requireThat(String(run[key]) === String(group[key]), `Orchestration ${key} differs from evidence`);
    }
    requireThat(run.status === 'completed' && run.total_units > 0 && run.units?.length === run.total_units,
        'Orchestration is incomplete');
    const counts = run.counts;
    requireThat(counts && counts.pending === 0 && counts.leased === 0 && counts.abandoned === 0 &&
        counts.retest_eligible === 0 && counts.completed_pass + counts.completed_fail + counts.completed_skipped === run.total_units,
        'Unresolved orchestration units remain');
    const memberTitles = pack.clusters.flatMap((cluster) => cluster.members.map((member) => member.full_title));
    const evidenceTitles = new Set(memberTitles);
    // The orchestration payload has no stable key/project, and evidence members
    // have no file. A title collision therefore cannot prove identity coverage.
    // Refuse ambiguous runs until those producer/reader dimensions are available.
    requireThat(memberTitles.every((title) => typeof title === 'string' && title.length > 0) &&
        evidenceTitles.size === memberTitles.length, 'Evidence titles ambiguously identify distinct tests');
    const coveredFailureTitles = new Set();
    let failed = 0;
    for (const unit of run.units) {
        requireThat(['completed_pass', 'completed_fail', 'completed_skipped'].includes(unit.state), 'Nonterminal unit');
        if (unit.state !== 'completed_fail') continue;
        failed++;
        const attempts = unit.attempts || [];
        const last = attempts.at(-1);
        requireThat(last && ['failed', 'timedOut'].includes(last.status) && last.reported_at &&
            Array.isArray(last.test_cases) && last.test_cases.length > 0, 'A worker failed without test evidence');
        const failures = last.test_cases.filter((test) => ['failed', 'timedOut', 'interrupted'].includes(test.status));
        requireThat(failures.length > 0 && failures.every((test) => test.status !== 'interrupted' && evidenceTitles.has(test.full_title)),
            'An orchestration failure is missing from the evidence pack');
        // Playwright can repeat a title for retries inside one unit. Different
        // failed units sharing it could instead be different missing spec files.
        for (const title of new Set(failures.map((test) => test.full_title))) {
            requireThat(!coveredFailureTitles.has(title), 'A failure title ambiguously covers distinct orchestration units');
            coveredFailureTitles.add(title);
        }
    }
    requireThat(failed > 0 && failed === counts.completed_fail, 'Red status has no fully represented failed units');
}

function validateStatus(status, context, decision) {
    requireThat(status?.id === context.status_id && status.state === 'failure', 'The assessed failing status changed');
    requireThat(status.creator?.login === 'github-actions[bot]', 'Status was not published by GitHub Actions');
    const url = new URL(status.target_url);
    const expectedPath = `/reports/mattermost/pr-${decision.pr_number}/${decision.head_sha.slice(0, 7)}/${encodeURIComponent(context.name)}`;
    requireThat(url.origin === new URL(BASE).origin && url.pathname === expectedPath &&
        url.searchParams.get('gh_run_id') === context.gh_run_id &&
        url.searchParams.get('gh_run_attempt') === context.gh_run_attempt,
        'Status target does not identify the assessed staging run and attempt');
}

export async function applyWaiver(decision, io) {
    requireThat(decision.version === VERSION && decision.repository === REPO && positive(decision.pr_number) &&
        sha(decision.head_sha) && sha(decision.merge_base_sha) && sha(decision.helper_revision), 'Invalid decision identity/version');
    requireThat(typeof decision.head_branch === 'string' && decision.head_branch.length > 0, 'Missing head branch');
    requireThat(Array.isArray(decision.contexts) && decision.contexts.length > 0 && decision.contexts.length <= 4 &&
        new Set(decision.contexts.map((context) => context.context)).size === decision.contexts.length, 'Invalid context set');
    const repoPath = `/repos/${REPO}`;
    const prPath = `${repoPath}/pulls/${decision.pr_number}`;
    const statusPath = `${repoPath}/commits/${decision.head_sha}/statuses`;
    const inspectPR = async () => {
        const pr = await io.github(prPath);
        requireThat(pr.state === 'open' && pr.base.ref === 'master' && pr.head.sha === decision.head_sha &&
            pr.head.ref === decision.head_branch, 'PR is closed, not based on master, or its head changed');
        return pr;
    };
    const pr = await inspectPR();
    const comparison = await io.github(`${repoPath}/compare/${pr.base.sha}...${decision.head_sha}`);
    requireThat(comparison.merge_base_commit?.sha === decision.merge_base_sha, 'Assessed merge base changed');
    const inspectRun = async (context) => {
        const run = await io.github(`${repoPath}/actions/runs/${context.gh_run_id}`);
        requireThat(run.head_sha === decision.head_sha && run.status === 'completed' &&
            ['failure', 'success'].includes(run.conclusion) && String(run.run_attempt) === context.gh_run_attempt &&
            run.event === 'workflow_dispatch' && run.path === '.github/workflows/e2e-tests-ci.yml',
            'Workflow head, attempt, event, path, or terminal state changed');
    };
    const latestStatuses = async () => {
        const rows = await io.paginate(statusPath);
        const latest = new Map();
        for (const row of rows) if (!latest.has(row.context)) latest.set(row.context, row);
        return latest;
    };
    const statuses = await latestStatuses();
    // Requiring the complete current failing full-context set prevents a partial
    // diagnosis being described as clearing the PR. New failures trigger a new run.
    const failing = [...statuses.values()].filter((status) => CONTEXTS.has(status.context) && status.state === 'failure').map((status) => status.context).sort();
    requireThat(same(failing, decision.contexts.map((context) => context.context).sort()), 'Not every failing full-test context was assessed');
    const proof = [];
    for (const context of decision.contexts) {
        requireThat(CONTEXTS.has(context.context) && positive(context.status_id) && positive(context.gh_run_id) &&
            positive(context.gh_run_attempt) && typeof context.name === 'string', 'Invalid context/run identity');
        requireThat(context.name === context.context.replace(/^e2e-test\//, '').replaceAll('/', '-'), 'Context/report name mapping differs');
        validateStatus(statuses.get(context.context), context, decision);
        await inspectRun(context);
        const evidence = await io.tsio(`/tests/evidence?${query({group_id: context.group_id})}`);
        const group = evidence.group;
        requireThat(group.repository === REPO && group.branch === `pr-${decision.pr_number}` &&
            group.commit_sha === decision.head_sha && group.gh_pr_number === Number(decision.pr_number) &&
            group.gh_run_id === context.gh_run_id && group.gh_run_attempt === context.gh_run_attempt && group.name === context.name,
            'Evidence group differs from the assessed PR run');
        requireThat(Number.isFinite(Date.parse(group.created_at)), 'Evidence needs its history cutoff timestamp');
        const keys = clusterKeys(evidence);
        requireThat(keys.length > 0 && Array.isArray(context.tests) &&
            same([...keys].sort(), context.tests.map((test) => test.stable_key).sort()), 'Every failing identity must be resolved exactly once');
        const orchestration = await io.tsio(`/orchestration/status?${query({repository: REPO, commit_sha: decision.head_sha,
            gh_run_id: context.gh_run_id, gh_run_attempt: context.gh_run_attempt, name: context.name})}`);
        validateOrchestration(orchestration, group, evidence);
        for (const test of context.tests) {
            validateReproduction(test, decision, group.environment_metadata || {});
            const baselineName = `${group.name}-master`;
            const history = await io.tsio(`/tests/history?${query({repo: REPO, baseline: 'true', branch: 'master',
                name: baselineName, framework: group.framework, test_id: test.stable_key, before: group.created_at, window: '30d', limit: '200'})}`);
            const latest = history.entries?.[0];
            requireThat(latest && latest.repository === REPO && latest.branch === 'master' && !latest.gh_pr_number &&
                latest.name === baselineName && latest.framework === group.framework && Date.parse(latest.created_at) <= Date.parse(group.created_at) &&
                io.now() - Date.parse(latest.created_at) >= 0 && io.now() - Date.parse(latest.created_at) <= 4 * 60 * 60 * 1000,
                'No fresh trusted baseline for the same suite and framework');
            const baseline = await io.tsio(`/tests/evidence?${query({group_id: test.baseline_group_id})}`);
            clusterKeys(baseline);
            const baselineEntry = history.entries.find((entry) => entry.group_id === test.baseline_group_id);
            requireThat(baselineEntry && io.now() - Date.parse(baselineEntry.created_at) >= 0 &&
                io.now() - Date.parse(baselineEntry.created_at) <= 4 * 60 * 60 * 1000 &&
                baseline.group.repository === REPO && !baseline.group.gh_pr_number && baseline.group.branch === 'master' &&
                Date.parse(baselineEntry.created_at) <= Date.parse(group.created_at) &&
                baseline.group.name === baselineName && baseline.group.framework === group.framework,
                'Baseline evidence is not in trusted same-suite history');
            const baselineCluster = baseline.clusters.find((cluster) => cluster.members.some((member) => member.stable_key === test.stable_key));
            const prCluster = evidence.clusters.find((cluster) => cluster.members.some((member) => member.stable_key === test.stable_key));
            requireThat(baselineCluster && baselineCluster.signature === prCluster.signature, 'Baseline failure mode differs');
            proof.push({context: context.context, stable_key: test.stable_key, history_sha256: digest(history), baseline_sha256: digest(baseline)});
        }
        proof.push({context: context.context, evidence_sha256: digest(evidence), orchestration_sha256: digest(orchestration)});
    }
    const record = {decision, proof};
    const decisionHash = digest(record);
    const body = `E2E: matching failures reproduced on the PR and its merge base.\n\nAutomatic waiver requested for the exact commit and contexts below. The diagnosis is an agent assessment, not a measured probability. Raw CI results remain unchanged.\n\nDecision: \`${decisionHash}\`\n<details><summary>Decision and evidence (recorded before status changes)</summary>\n\n\`\`\`json\n${JSON.stringify(record, null, 2).replaceAll('<', '\\u003c')}\n\`\`\`\n</details>`;
    requireThat(body.length <= 60000, 'Decision exceeds comment limit; reduce logs without omitting failures');
    const comment = await io.github(`${repoPath}/issues/${decision.pr_number}/comments`, {body});
    requireThat(positive(comment.id) && comment.html_url === `https://github.com/${REPO}/pull/${decision.pr_number}#issuecomment-${comment.id}`,
        'Durable decision comment was not confirmed');
    // Re-read the durable object. A transport success is not enough.
    const saved = await io.github(`${repoPath}/issues/comments/${comment.id}`);
    requireThat(saved.body === body, 'Stored decision differs from the submitted record');
    const written = [];
    const restoreFailure = async (context) => {
        await io.github(`${repoPath}/statuses/${decision.head_sha}`, {
            state: 'failure', context: context.context,
            description: `Waiver invalidated; inspect decision ${decisionHash.slice(0, 12)}`, target_url: comment.html_url,
        });
    };
    for (const context of decision.contexts) {
        await inspectPR();
        await inspectRun(context);
        validateStatus((await latestStatuses()).get(context.context), context, decision);
        try {
            const result = await io.github(`${repoPath}/statuses/${decision.head_sha}`, {
                state: 'success', context: context.context, description: `Verified baseline failure; decision ${decisionHash.slice(0, 12)}`,
                target_url: comment.html_url,
            });
            requireThat(result.state === 'success' && result.context === context.context && positive(result.id), 'Status write was not confirmed');
            written.push({context: context.context, status_id: result.id});
            const actual = (await latestStatuses()).get(context.context);
            requireThat(actual?.id === result.id && actual.state === 'success', 'Written status was superseded; inspect the run');
            await inspectRun(context);
        } catch (error) {
            // GitHub statuses have no compare-and-set. If a rerun races the POST,
            // restore red; never leave a detected stale waiver as success.
            try { await restoreFailure(context); } catch {
                fail(`Status may be green: restoration failed after ${error.message}; inspect ${comment.html_url}`);
            }
            fail(`Status restored to failure: ${error.message}`);
        }
    }
    const receipt = {decision_sha256: decisionHash, head_sha: decision.head_sha, comment_url: comment.html_url, statuses: written};
    await io.github(`${repoPath}/issues/${decision.pr_number}/comments`, {body: `E2E waiver applied and verified on \`${decision.head_sha}\`.\n\n[Decision recorded before the waiver](${comment.html_url})\n\n\`\`\`json\n${JSON.stringify(receipt, null, 2)}\n\`\`\``});
    return receipt;
}

export function productionIO(token) {
    requireThat(token, 'GH_LABEL_TOKEN requires pull_requests:write and statuses:write; no token, no waiver');
    async function request(url, payload, authenticated) {
        const headers = {Accept: authenticated ? 'application/vnd.github+json' : 'application/json'};
        if (authenticated) headers['X-GitHub-Api-Version'] = '2026-03-10';
        if (authenticated) headers.Authorization = `Bearer ${token}`;
        if (payload) headers['Content-Type'] = 'application/json';
        const response = await fetch(url, {method: payload ? 'POST' : 'GET', headers,
            body: payload ? JSON.stringify(payload) : undefined, redirect: 'error', signal: AbortSignal.timeout(30000)});
        requireThat(response.ok && response.headers.get('content-type')?.includes('application/json'), `Request failed (${response.status}) at ${new URL(url).pathname}`);
        return response.json();
    }
    return {
        now: () => Date.now(),
        github: (path, payload) => request(`https://api.github.com${path}`, payload, true),
        tsio: (path) => request(`${BASE}${path}`, undefined, false),
        paginate: async (path) => {
            const rows = [];
            for (let page = 1; page <= 100; page++) {
                const batch = await request(`https://api.github.com${path}?per_page=100&page=${page}`, undefined, true);
                requireThat(Array.isArray(batch), 'Unexpected GitHub list response');
                rows.push(...batch);
                if (batch.length < 100) return rows;
            }
            fail('GitHub status pagination exceeded the safety bound');
        },
    };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
    try {
        requireThat(process.argv.length === 3, 'Usage: node apply-e2e-waiver.mjs decision.json');
        const decision = JSON.parse(await readFile(process.argv[2], 'utf8'));
        const receipt = await applyWaiver(decision, productionIO(process.env.GH_LABEL_TOKEN));
        process.stdout.write(`${JSON.stringify(receipt, null, 2)}\n`);
    } catch (error) {
        process.stderr.write(`Waiver stopped: ${error.message}\n`);
        process.exitCode = 1;
    }
}
