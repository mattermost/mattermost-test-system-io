#!/usr/bin/env bash
#
# E2E flakiness management — end-to-end demo.
#
# Builds a throwaway database, migrates it to head, seeds seven scenarios,
# starts the real TSIO server, and drives the real /triage/evidence endpoint
# and the real decide()/canWaive() from the action. Nothing is simulated except
# the failure data.
#
# Usage:
#   bash docs/superpowers/specs/r7-demo/demo.sh            # full demo
#   bash docs/superpowers/specs/r7-demo/demo.sh --keep     # leave server+DB up
#   bash docs/superpowers/specs/r7-demo/demo.sh --cleanup  # tear down only
#
# Requires: Docker running with the tsio dev postgres up, Go, Node, jq.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
DEMO_DIR="${REPO_ROOT}/docs/superpowers/specs/r7-demo"
ACTION_DIR="${REPO_ROOT}/.github/actions/test-system-io-ai-triage"
WORK="${TMPDIR:-/tmp}/tsio-r7-demo"

PG_CONTAINER="${PG_CONTAINER:-tsio-dev-postgres-1}"
PG_PORT="${PG_PORT:-6432}"
DB="${DB:-tsio_r7demo}"
PORT="${PORT:-8099}"
DB_URL="postgres://tsio:tsio@localhost:${PG_PORT}/${DB}?sslmode=disable"
API="http://localhost:${PORT}/api/v1"

# gvm's GOROOT shadows homebrew's; pin the toolchain explicitly.
GO="${GO:-/opt/homebrew/opt/go/libexec/bin/go}"
[[ -x "$GO" ]] || GO="$(command -v go)"
export GOROOT="${GOROOT:-$(dirname "$(dirname "$GO")")}"
export GVM_ROOT=""

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
head1() { printf '\n\033[1;36m=== %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✔\033[0m %s\n' "$*"; }
bad()  { printf '  \033[31m✘\033[0m %s\n' "$*"; }

psql_() { docker exec -i "$PG_CONTAINER" psql -U tsio -d "$1" "${@:2}"; }

cleanup() {
  pkill -f "${WORK}/tsio-server" 2>/dev/null || true
  psql_ postgres -q -c "DROP DATABASE IF EXISTS ${DB};" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}

if [[ "${1:-}" == "--cleanup" ]]; then
  bold "Tearing down…"; cleanup; ok "done"; exit 0
fi
[[ "${1:-}" == "--keep" ]] || trap cleanup EXIT

mkdir -p "$WORK"

# ---------------------------------------------------------------------------
head1 "0. Preflight"
docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER" \
  || { bad "postgres container '$PG_CONTAINER' is not running"; exit 1; }
ok "postgres container up"
command -v jq >/dev/null || { bad "jq is required"; exit 1; }
ok "$($GO version)"
ulimit -n 4096 2>/dev/null || true

# ---------------------------------------------------------------------------
head1 "1. Throwaway database, migrated to head"
psql_ postgres -q -c "DROP DATABASE IF EXISTS ${DB};" -c "CREATE DATABASE ${DB} OWNER tsio;" >/dev/null 2>&1
( cd "${REPO_ROOT}/apps/server" && TSIO_DATABASE_URL="$DB_URL" "$GO" run ./cmd/tsioctl db migrate >/dev/null )
ok "migrated to $(psql_ "$DB" -tAc 'select max(version) from schema_migrations')"

head1 "2. Seed the scenarios"
for f in seed_helper.sql seed_data.sql seed_ef.sql seed_g.sql; do
  [[ -f "${DEMO_DIR}/${f}" ]] || { bad "missing ${f}"; exit 1; }
  psql_ "$DB" -q < "${DEMO_DIR}/${f}" >/dev/null
done
ok "$(psql_ "$DB" -tAc 'select count(distinct external_test_id) from test_cases') tests, $(psql_ "$DB" -tAc 'select count(*) from report_groups') runs"

head1 "3. Start the real TSIO server"
( cd "${REPO_ROOT}/apps/server" && "$GO" build -o "${WORK}/tsio-server" ./cmd/tsio )
TSIO_ENVIRONMENT=development TSIO_DATABASE_URL="$DB_URL" TSIO_HTTP_LISTEN_ADDR=":${PORT}" \
TSIO_S3_ENDPOINT=http://localhost:9100 TSIO_S3_REGION=us-east-1 TSIO_S3_BUCKET=tsio \
TSIO_S3_ACCESS_KEY=minioadmin TSIO_S3_SECRET_KEY=minioadmin TSIO_S3_FORCE_PATH_STYLE=true \
TSIO_SESSION_SECRET=demo-secret-demo-secret-demo-secret-0123 TSIO_GITHUB_OIDC_ENABLED=false \
  "${WORK}/tsio-server" > "${WORK}/server.log" 2>&1 &
for _ in $(seq 1 30); do
  curl -fsS "${API}/triage/pass-rates?repo=mattermost/mattermost" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "${API}/triage/pass-rates?repo=mattermost/mattermost" >/dev/null \
  || { bad "server did not come up; see ${WORK}/server.log"; tail -20 "${WORK}/server.log"; exit 1; }
ok "listening on :${PORT}"

KEY="$( cd "${REPO_ROOT}/apps/server" && TSIO_DATABASE_URL="$DB_URL" \
  "$GO" run ./cmd/tsioctl keys issue --name r7demo 2>/dev/null | awk '/plaintext:/{print $2}' )"
[[ -n "$KEY" ]] || { bad "could not issue an API key"; exit 1; }
ok "API key issued"

# ---------------------------------------------------------------------------
# GOAL 1 — is this failure flaky, or mine?
# ---------------------------------------------------------------------------
head1 "GOAL 1 — 'is this flaky, or did my PR break it?'"
echo "  Real evidence packs -> real decide()/canWaive(). Expected check state in brackets."
echo

# macOS ships bash 3.2, which has no associative arrays — look the id up per call.
gid_for() {
  psql_ "$DB" -tAc "select id from report_groups where gh_run_id = '$1'" | tr -d '[:space:]'
}

fetch_pack() { # $1=tag $2=run_id
  local id
  id="$(gid_for "$2")"
  [[ -n "$id" ]] || { bad "no report group for run $2"; exit 1; }
  curl -fsS "${API}/triage/evidence?group_id=${id}&baseline_branch=main" -o "${WORK}/ev_$1.json"
}
for pair in "A:run-pr5001-3" "B:run-pr5002-3" "C:run-pr5003-3" "D:run-pr5004-1" \
            "E:run-pr5005-1" "F:run-pr5006-3" "G:run-pr511-1"; do
  fetch_pack "${pair%%:*}" "${pair#*:}"
done
ok "seven evidence packs fetched from the live API"
echo

cp "${DEMO_DIR}/r7demo_driver.ts" "${ACTION_DIR}/src/r7demo_driver.ts"
( cd "$ACTION_DIR" && SP="$WORK" node --experimental-strip-types src/r7demo_driver.ts 2>/dev/null )
rm -f "${ACTION_DIR}/src/r7demo_driver.ts"

# ---------------------------------------------------------------------------
# GOAL 2 — master health
# ---------------------------------------------------------------------------
head1 "GOAL 2 — 'watch master and fix the flaky tests'"

echo
bold "  2a. The RAW pass-rate — waiving can never improve it"
curl -fsS "${API}/triage/pass-rates?repo=mattermost/mattermost&branch=main&window=30d" \
  | jq -r '"     raw_pass_rate=\(.raw_pass_rate|.*100|round/100)%  raw_failures=\(.raw_failures)  waived=\(.waived_failures)  effective=\(.effective_failures)"'

echo
bold "  2b. Alerts — which master failure needs a human now"
curl -fsS "${API}/triage/alerts/evaluation?repo=mattermost/mattermost&branch=main" \
  | jq -r '.alerts[] | "     \(.rule)  subject=\(.subject)  severity=\(.severity)  streak=\(.evidence.streak // "-")/\(.evidence.total_runs // "-")"'

echo
bold "  2c. The fix queue, ranked by BLAST RADIUS (R7-L2)"
echo "     Distinct PRs a test broke — realized developer cost, not 'most broken on master'."
curl -fsS -H "X-API-Key: ${KEY}" "${API}/triage/stabilization/queue?repo=mattermost/mattermost" \
  | jq -r '.ranked[] | "     \(.test_id)   affected_PRs=\(.affected_prs)   master_failed=\(.failed)   rate=\((.failure_rate*100)|round)%"'

echo
bold "  2d. Is the loop keeping up? (R7-L1)"
curl -fsS "${API}/triage/stabilization/throughput?repo=mattermost/mattermost&window=30d" \
  | jq -r '"     arrival=\(.arrival_rate_per_day)/day   drain=\(.modeled_drain_rate_per_day)/day   coverage=\(.coverage_pct)%",
           "     cycle=\(.cycle_days)d (review \(.review_latency_days)d + window \(.window_days)d)",
           "     required_concurrency=\(.required_concurrency) vs cap \(.max_concurrency)   keeping_up=\(.keeping_up)",
           "     binding lever: \(.binding_lever)",
           "     -> \(.recommendation)"'

echo
bold "  2e. Same question at a 48-hour review SLA — the one real lever"
curl -fsS "${API}/triage/stabilization/throughput?repo=mattermost/mattermost&window=30d&review_latency_days=2&concurrency=5" \
  | jq -r '"     drain=\(.modeled_drain_rate_per_day)/day   coverage=\(.coverage_pct)%   cycle=\(.cycle_days)d"'

# ---------------------------------------------------------------------------
# GOAL 3 — it came from master, and here is who
# ---------------------------------------------------------------------------
head1 "GOAL 3 — 'it came from master, and it is a real bug'"
jq -r '.clusters[0].representative
       | "     test=\(.external_test_id)",
         "     last_pass      = \(.history.last_pass_commit)",
         "     failing_since  = \(.history.failing_since_commit)",
         "     -> author attribution reads the commit range between those two"' "${WORK}/ev_D.json"
echo "     The same failure on a MAIN run is shown in the GOAL 1 table above: never waived."

# ---------------------------------------------------------------------------
# LEVER 3 — quarantine
# ---------------------------------------------------------------------------
head1 "LEVER 3 — quarantine: owned, expiring, and it cannot hide anything"

echo
bold "  3a. Refused without the guardrails"
for body in '{"test_id":"MM-T2007","reason":"x","days":7}' \
            '{"test_id":"MM-T2007","owner":"@ti","reason":"x"}' \
            '{"test_id":"MM-T2007","owner":"@ti","reason":"x","days":365}'; do
  msg="$(curl -fsS -X POST -H "X-API-Key: ${KEY}" -H 'content-type: application/json' \
        -d "$body" "${API}/triage/quarantine?repo=mattermost/mattermost" 2>/dev/null \
        || curl -sS -X POST -H "X-API-Key: ${KEY}" -H 'content-type: application/json' \
           -d "$body" "${API}/triage/quarantine?repo=mattermost/mattermost" | jq -r .message)"
  echo "     refused: ${msg}"
done

echo
bold "  3b. Quarantine the top of the queue"
curl -fsS -X POST -H "X-API-Key: ${KEY}" -H 'content-type: application/json' \
  -d '{"test_id":"MM-T2007","owner":"@test-infra","reason":"15% on master but hits every PR smoke shard; queued for a fix","days":14}' \
  "${API}/triage/quarantine?repo=mattermost/mattermost" \
  | jq -r '"     owner=\(.owner)  active=\(.active)  \(.days_remaining)d left  created_by=\(.created_by)"'

echo
bold "  3c. What it did NOT change"
curl -fsS "${API}/triage/pass-rates?repo=mattermost/mattermost&branch=main&window=30d" \
  | jq -r '"     raw_pass_rate still \(.raw_pass_rate|.*100|round/100)% — quarantine cannot improve it"'
curl -fsS -H "X-API-Key: ${KEY}" "${API}/triage/stabilization/queue?repo=mattermost/mattermost" \
  | jq -r '"     still #\(([.ranked[].test_id]|index("MM-T2007"))+1) in the fix queue — it buys time, not forgiveness"'

echo
bold "  3d. The PR check for a quarantined test — PR green, master still red"
fetch_pack G run-pr511-1
cat > "${ACTION_DIR}/src/qdemo.ts" <<'TS'
import { decide } from "./policy.ts";
import type { EvidencePack } from "./types.ts";
import { readFileSync } from "node:fs";
const pack: EvidencePack = JSON.parse(readFileSync(process.env.EV!, "utf8"));
const c = pack.clusters[0]!;
for (const runType of ["PR", "MAIN"]) {
  const d = decide({
    failure: c.representative,
    runType,
    branch: runType === "MAIN" ? "main" : pack.group.branch,
    changedFiles: ["webapp/channels/src/components/unrelated/thing.tsx"],
    phase: 2,
  });
  console.log(`     ${runType.padEnd(5)} -> ${d.check_state.toUpperCase().padEnd(8)} ${d.reason}`);
}
TS
( cd "$ACTION_DIR" && EV="${WORK}/ev_G.json" node --experimental-strip-types src/qdemo.ts 2>/dev/null )
rm -f "${ACTION_DIR}/src/qdemo.ts"

# ---------------------------------------------------------------------------
# TRUST — the ledger and the blind audit
# ---------------------------------------------------------------------------
head1 "TRUST 1 — the ledger: nothing greened silently"
post_verdict() {
  curl -fsS -X POST -H "X-API-Key: ${KEY}" -H 'content-type: application/json' -d "$1" \
    "${API}/triage/verdicts" >/dev/null
}
post_verdict '{"repository":"mattermost/mattermost","branch":"feat/search-tweak","commit_sha":"pr5001ccc","gh_run_id":"run-pr5001-3","gh_pr_number":5001,"model":"claude-opus-5","verdicts":[{"external_test_id":"MM-T2001","verdict":"FLAKY_TEST","confidence":0.9,"waived":true,"check_state":"success","root_cause":"chronic timing race; rate did not shift (1-of-3, p=0.784)","evidence":[{"cite":"history","runs":20,"failed":8},{"cite":"rate_shift","p_value":0.784,"shifted":false}]}]}'
post_verdict '{"repository":"mattermost/mattermost","branch":"feat/draft-refactor","commit_sha":"pr5002aaa","gh_run_id":"run-pr5002-3","gh_pr_number":5002,"model":"claude-opus-5","verdicts":[{"external_test_id":"MM-T2002","verdict":"PR_REGRESSION","confidence":0.95,"waived":false,"check_state":"failure","root_cause":"draft did not persist; stack names drafts.tsx which this PR edits","suspect_commit":"pr5002aaa","evidence":[{"cite":"rate_shift","p_value":0.0,"shifted":true}]}]}'
post_verdict '{"repository":"mattermost/mattermost","branch":"cherry-pick-abac","commit_sha":"pr5003aaa","gh_run_id":"run-pr5003-3","gh_pr_number":5003,"model":"claude-opus-5","verdicts":[{"external_test_id":"MM-T5824","verdict":"FLAKY_INFRA","confidence":0.88,"waived":false,"check_state":"failure","root_cause":"rate shifted (3-of-3, p=0.064) so chronic flakiness does not explain it","evidence":[{"cite":"rate_shifted_at_commit","p_value":0.064,"alpha":0.1}]}]}'
curl -fsS -H "X-API-Key: ${KEY}" "${API}/triage/verdicts?repo=mattermost/mattermost&window=30d" \
  | jq -r '(.verdicts // .rows // .)[] | "     \(.external_test_id)  PR:\(.gh_pr_number // "master")  \(.verdict)  waived=\(.waived)  check=\(.check_state)\n        why: \(.root_cause)"'
echo
psql_ "$DB" -tAc "select '     '||external_test_id||'  evidence_items='||jsonb_array_length(coalesce(evidence,'[]'::jsonb)) from triage_verdicts order by external_test_id"

head1 "TRUST 2 — the blind audit: the payload does not contain the verdict"
curl -fsS -H "X-API-Key: ${KEY}" "${API}/triage/audit/sample?repo=mattermost/mattermost&window=30d" -o "${WORK}/audit.json" || true
if jq -e '.items' "${WORK}/audit.json" >/dev/null 2>&1; then
  echo "     raw first item, exactly as the reviewer's browser receives it:"
  jq -c '.items[0]' "${WORK}/audit.json" | sed 's/^/       /'
  echo "     banned keys present?"
  for k in verdict confidence root_cause model stratum suspect_commit; do
    if jq -e --arg k "$k" '.items[0] | has($k)' "${WORK}/audit.json" >/dev/null 2>&1; then
      bad "$k LEAKED"
    else
      ok "$k absent"
    fi
  done
else
  echo "     (no audit sample yet — the sampler needs waived verdicts older than its floor)"
fi

# ---------------------------------------------------------------------------
head1 "Done"
if [[ "${1:-}" == "--keep" ]]; then
  echo "  Server still up on :${PORT}, database '${DB}' retained."
  echo "  API key: ${KEY}"
  echo "  Tear down with: bash ${BASH_SOURCE[0]} --cleanup"
else
  echo "  Cleaning up (pass --keep to leave the server and DB running)."
fi
