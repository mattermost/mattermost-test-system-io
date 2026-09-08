import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyWaiver, VERSION } from './apply-e2e-waiver.mjs';

const head = 'a'.repeat(40);
const base = 'b'.repeat(40);
const image = `example.com/mattermost@sha256:${'a'.repeat(64)}`;
const context = 'e2e-test/cypress-full/enterprise';
const name = 'cypress-full-enterprise';

function fixture() {
    const now = Date.now();
    const runs = () => Array.from({length: 3}, () => ({outcome: 'failed', retries: 0,
        log: 'MM-T1: expected image visible; actual image absent. Exit code 1.'}));
    const decision = {version: VERSION, repository: 'mattermost/mattermost', pr_number: 38356,
        head_sha: head, merge_base_sha: base, helper_revision: 'c'.repeat(40), head_branch: 'feature/triage',
        contexts: [{context, name, gh_run_id: '123', gh_run_attempt: '1', group_id: 'pr-group', status_id: 100,
            tests: [{stable_key: 'MM-T1', baseline_group_id: 'base-group', verdict: 'BASELINE_REPRODUCED',
                diff_suspect: false, diff_reason: 'Changed reporting destination does not alter image rendering.',
                reproduction: {pr_sha: head, base_sha: base, pr_image: image, base_image: image,
                    base_image_commit: base, environment_matches_ci: true, credentials_isolated: true,
                    same_failure_mode: true, unexplained_failures: 0, pr_runs: runs(), base_runs: runs()}}]}]};
    const pr = {state: 'open', base: {ref: 'master', sha: base}, head: {sha: head, ref: 'feature/triage'}};
    const group = {repository: 'mattermost/mattermost', commit_sha: head, gh_pr_number: 38356, branch: 'pr-38356',
        created_at: new Date(now).toISOString(),
        name, gh_run_id: '123', gh_run_attempt: '1', framework: 'cypress', status: 'completed',
        total_reports_expected: 1, reports_registered: 1, reports_complete: 1, environment_metadata: {server_image_digest: image}};
    const member = {stable_key: 'MM-T1', full_title: 'MM-T1 image renders'};
    const evidence = {group, complete: true, truncated: false, cluster_count: 1, failure_count: 1,
        clusters: [{signature: 'same-signature', member_count: 1, members: [member]}]};
    const baseline = structuredClone(evidence);
    Object.assign(baseline.group, {commit_sha: base, gh_pr_number: null, branch: 'master', name: `${name}-master`});
    const history = {entries: [{group_id: 'base-group', repository: group.repository, framework: 'cypress',
        branch: 'master', name: `${name}-master`, created_at: new Date(now - 60000).toISOString()}]};
    const orchestration = {...group, status: 'completed', total_units: 1,
        counts: {pending: 0, leased: 0, abandoned: 0, retest_eligible: 0, completed_pass: 0, completed_fail: 1, completed_skipped: 0},
        units: [{state: 'completed_fail', attempts: [{status: 'failed', reported_at: new Date(now).toISOString(),
            test_cases: [{full_title: member.full_title, status: 'failed'}]}]}]};
    const run = {head_sha: head, status: 'completed', conclusion: 'failure', run_attempt: 1,
        event: 'workflow_dispatch', path: '.github/workflows/e2e-tests-ci.yml'};
    const state = {pr, evidence, baseline, history, orchestration, run, mutations: [],
        statuses: [{id: 100, context, state: 'failure', creator: {login: 'github-actions[bot]'},
            target_url: `https://staging-test-io.test.mattermost.com/reports/mattermost/pr-38356/${head.slice(0, 7)}/${name}?gh_run_id=123&gh_run_attempt=1`}],
        comments: new Map(), afterRecord: () => {}, afterWrite: () => {}, afterStatusList: () => {}};
    const io = {
        now: () => now,
        tsio: async (path) => {
            if (path.startsWith('/orchestration/status?')) return state.orchestration;
            if (path.startsWith('/tests/history?')) {
                const params = new URL(path, 'https://example.com').searchParams;
                assert.equal(params.get('baseline'), 'true');
                assert.equal(params.get('name'), `${name}-master`);
                assert.equal(params.get('before'), group.created_at);
                assert.equal(params.get('repo'), 'mattermost/mattermost');
                return state.history;
            }
            return path.endsWith('group_id=pr-group') ? state.evidence : state.baseline;
        },
        paginate: async () => {
            const snapshot = structuredClone(state.statuses);
            state.afterStatusList();
            return snapshot;
        },
        github: async (path, payload) => {
            if (payload) state.mutations.push({path, payload});
            if (path.includes('/compare/')) return {merge_base_commit: {sha: base}};
            if (path.includes('/actions/runs/')) return state.run;
            if (path.endsWith('/pulls/38356')) return state.pr;
            if (path.endsWith('/issues/38356/comments')) {
                if (state.failComment) throw Error('Comment write failed');
                const id = state.comments.size + 1;
                state.comments.set(id, payload);
                state.afterRecord();
                return {id, html_url: `https://github.com/mattermost/mattermost/pull/38356#issuecomment-${id}`};
            }
            if (path.includes('/issues/comments/')) return state.comments.get(Number(path.split('/').at(-1)));
            if (path.endsWith(`/statuses/${head}`)) {
                const status = {id: 200 + state.mutations.length, ...payload};
                state.statuses.unshift(status);
                state.afterWrite();
                return status;
            }
            throw Error(`Unexpected endpoint ${path}`);
        },
    };
    return {decision, state, io};
}

const writes = (state) => state.mutations.filter((mutation) => mutation.path.includes('/statuses/'));

test('valid waiver records and rereads decision before immutable SHA status, then verifies and posts receipt', async () => {
    const {decision, state, io} = fixture();
    const receipt = await applyWaiver(decision, io);
    assert.equal(receipt.head_sha, head);
    assert.equal(state.mutations.length, 3);
    assert.match(state.mutations[0].path, /issues\/38356\/comments$/);
    assert.match(state.mutations[0].payload.body, /recorded before status changes/);
    assert.equal(writes(state).length, 1);
    assert.equal(writes(state)[0].path, `/repos/mattermost/mattermost/statuses/${head}`);
    assert.equal(writes(state)[0].payload.context, context);
    assert.equal(writes(state)[0].payload.target_url, receipt.comment_url);
    assert.equal(state.comments.size, 2);
});

for (const [title, change, error] of [
    ['raw head branch in report URL', (s) => {s.statuses[0].target_url = s.statuses[0].target_url.replace('/pr-38356/', '/feature~triage/');}, /Status target/],
    ['incorrect synthetic evidence branch', (s) => {s.evidence.group.branch = 'pr-123';}, /Evidence group/],
    ['new PR head', (s) => {s.pr.head.sha = 'd'.repeat(40);}, /head changed/],
    ['different base branch', (s) => {s.pr.base.ref = 'release-11';}, /master/],
    ['new workflow attempt', (s) => {s.run.run_attempt = 2;}, /attempt/],
    ['changed status ID', (s) => {s.statuses[0].id = 101;}, /status changed/],
    ['incomplete ingestion', (s) => {s.evidence.complete = false;}, /incomplete/],
    ['missing uploaded shard', (s) => {s.evidence.group.total_reports_expected = 2;}, /coverage/],
    ['hidden cluster member', (s) => {s.evidence.clusters[0].member_count = 2;}, /hides members/],
    ['missing worker report', (s) => {s.orchestration.units[0].attempts[0].test_cases = [];}, /without test evidence/],
    ['unrepresented failed test', (s) => {s.orchestration.units[0].attempts[0].test_cases[0].full_title = 'another test';}, /missing from/],
    ['interrupted worker', (s) => {s.orchestration.units[0].attempts[0].status = 'interrupted';}, /without test evidence/],
    ['pending retest', (s) => {s.orchestration.counts.retest_eligible = 1;}, /Unresolved/],
    ['fork master history', (s) => {s.history.entries[0].gh_pr_number = 123;}, /trusted baseline/],
    ['stale master history', (s) => {s.history.entries[0].created_at = '2000-01-01T00:00:00Z';}, /fresh trusted/],
    ['future baseline observation', (s) => {s.history.entries[0].created_at = new Date(Date.now() + 60000).toISOString();}, /fresh trusted/],
    ['different baseline failure', (s) => {s.baseline.clusters[0].signature = 'different';}, /mode differs/],
    ['failed audit write', (s) => {s.failComment = true;}, /Comment write failed/],
]) {
    test(`refuses ${title} without any status write`, async () => {
        const {decision, state, io} = fixture();
        change(state);
        await assert.rejects(applyWaiver(decision, io), error);
        assert.equal(writes(state).length, 0);
    });
}

for (const [title, change, error] of [
    ['new head', (s) => {s.pr.head.sha = 'e'.repeat(40);}, /head changed/],
    ['rerun', (s) => {s.run.run_attempt = 2;}, /attempt/],
    ['new status', (s) => {s.statuses[0].id++;}, /status changed/],
    ['altered stored reason', (s) => {s.comments.get(1).body = 'changed';}, /Stored decision differs/],
]) {
    test(`refuses ${title} arriving after the durable record`, async () => {
        const {decision, state, io} = fixture();
        state.afterRecord = () => change(state);
        await assert.rejects(applyWaiver(decision, io), error);
        assert.equal(writes(state).length, 0);
    });
}

for (const [title, change, error] of [
    ['missing failure', (d) => {d.contexts[0].tests = [];}, /Every failing/],
    ['related diff', (d) => {d.contexts[0].tests[0].diff_suspect = true;}, /diff vetoes/],
    ['history-only verdict', (d) => {d.contexts[0].tests[0].verdict = 'KNOWN_FLAKE';}, /paired reproduction/],
    ['mutable image', (d) => {d.contexts[0].tests[0].reproduction.pr_image = 'example:master';}, /immutable images/],
    ['missing base failure', (d) => {d.contexts[0].tests[0].reproduction.base_runs.forEach((r) => {r.outcome = 'passed';});}, /actually reproduce/],
    ['enabled retries', (d) => {d.contexts[0].tests[0].reproduction.pr_runs[0].retries = 1;}, /retries disabled/],
    ['unisolated token', (d) => {d.contexts[0].tests[0].reproduction.credentials_isolated = false;}, /credential isolation/],
]) {
    test(`refuses ${title}`, async () => {
        const {decision, state, io} = fixture();
        change(decision);
        await assert.rejects(applyWaiver(decision, io), error);
        assert.equal(writes(state).length, 0);
    });
}

test('a push in the API write gap never redirects the write to the new head', async () => {
    const {decision, state, io} = fixture();
    let lists = 0;
    state.afterStatusList = () => { if (++lists === 2) state.pr.head.sha = 'f'.repeat(40); };
    const result = await applyWaiver(decision, io);
    assert.equal(result.head_sha, head);
    assert.equal(writes(state)[0].path, `/repos/mattermost/mattermost/statuses/${head}`);
});

test('a same-SHA rerun in the non-atomic GitHub write gap is detected and restored to failure', async () => {
    const {decision, state, io} = fixture();
    state.afterWrite = () => {state.run.run_attempt = 2;};
    await assert.rejects(applyWaiver(decision, io), /attempt/);
    assert.equal(writes(state).length, 2);
    assert.equal(state.statuses[0].state, 'failure');
    assert.equal(state.comments.size, 1);
});


test('refuses one evidence identity reused across distinct failed spec units', async () => {
    const {decision, state, io} = fixture();
    state.orchestration.total_units = 2;
    state.orchestration.counts.completed_fail = 2;
    state.orchestration.units[0].spec_path = 'specA.cy.js';
    state.orchestration.units.push(structuredClone(state.orchestration.units[0]));
    state.orchestration.units[1].spec_path = 'specB.cy.js';
    await assert.rejects(applyWaiver(decision, io), /ambiguously covers distinct orchestration units/);
    assert.equal(writes(state).length, 0);
});

test('refuses distinct evidence identities sharing an ambiguous title', async () => {
    const {decision, state, io} = fixture();
    const cluster = state.evidence.clusters[0];
    cluster.members.push({...cluster.members[0], stable_key: 'specB.cy.js :: same title'});
    cluster.member_count = 2;
    state.evidence.failure_count = 2;
    decision.contexts[0].tests.push({...decision.contexts[0].tests[0], stable_key: cluster.members[1].stable_key});
    await assert.rejects(applyWaiver(decision, io), /Evidence titles ambiguously identify distinct tests/);
    assert.equal(writes(state).length, 0);
});

test('repeated failed retries within one unit do not create an ambiguous second unit', async () => {
    const {decision, state, io} = fixture();
    const cases = state.orchestration.units[0].attempts[0].test_cases;
    cases.push({...cases[0], retry_count: 1});
    await applyWaiver(decision, io);
    assert.equal(writes(state).length, 1);
});
