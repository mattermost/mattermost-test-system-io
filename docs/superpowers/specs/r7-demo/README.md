# Round-7 live demo harness

**Committed on purpose.** Rounds 2, 3 and 6 each lost work by leaving it
uncommitted, and rounds 4–6's numbers are unreproducible because the harness and
dataset were never checked in. This directory is the whole reproduction.

It seeds six scenarios into a throwaway database, drives the **real** TSIO
server and the **real** `decide()`/`canWaive`, and prints the check state for
each. Nothing is simulated except the failure data itself.

## What it proves

| Scenario | Test | Setup | Expected check |
|---|---|---|---|
| A | MM-T2001 | 40% flake on master, 1-of-3 here (unshifted) | SUCCESS |
| B | MM-T2002 | clean master, 3-of-3 here, stack names the edited file | FAILURE |
| C | MM-T5824 | ABAC: 40% flake **and** 3-of-3 here (shifted), CI-only diff | FAILURE |
| D | MM-T2004 | bystander PR on an already-broken master test | SUCCESS |
| E | MM-T2005 | failed then recovered on retry, 5% on master | SUCCESS |
| F | MM-T2006 | 10% flake, 1-of-3 here (unshifted) | SUCCESS |

Plus, from a MAIN run on scenario D: `MAIN_REGRESSION`, never waived, with
`last_pass` / `failing_since` for author attribution.

## Reproduce

```bash
export GOROOT=/opt/homebrew/opt/go/libexec
export PATH=$GOROOT/bin:$PATH
ulimit -n 4096
```

1. Throwaway database, migrated to head:

```bash
docker exec tsio-dev-postgres-1 psql -U tsio -d postgres -c "DROP DATABASE IF EXISTS tsio_r7demo;" -c "CREATE DATABASE tsio_r7demo OWNER tsio;"
```

```bash
cd apps/server && TSIO_DATABASE_URL="postgres://tsio:tsio@localhost:6432/tsio_r7demo?sslmode=disable" go run ./cmd/tsioctl db migrate
```

2. Seed (order matters — the helper first):

```bash
for f in seed_helper.sql seed_data.sql seed_ef.sql; do docker exec -i tsio-dev-postgres-1 psql -U tsio -d tsio_r7demo < docs/superpowers/specs/r7-demo/$f; done
```

3. Server on a spare port:

```bash
cd apps/server && TSIO_ENVIRONMENT=development TSIO_DATABASE_URL="postgres://tsio:tsio@localhost:6432/tsio_r7demo?sslmode=disable" TSIO_HTTP_LISTEN_ADDR=":8099" TSIO_S3_ENDPOINT=http://localhost:9100 TSIO_S3_REGION=us-east-1 TSIO_S3_BUCKET=tsio TSIO_S3_ACCESS_KEY=minioadmin TSIO_S3_SECRET_KEY=minioadmin TSIO_S3_FORCE_PATH_STYLE=true TSIO_SESSION_SECRET=demo-secret-demo-secret-demo-secret-0123 TSIO_GITHUB_OIDC_ENABLED=false go run ./cmd/tsio
```

4. Pull an evidence pack per scenario into `$SP/ev_<TAG>.json` (group ids come
   from `select gh_pr_number, id from report_groups where gh_pr_number is not null`):

```bash
curl -s "http://localhost:8099/api/v1/triage/evidence?group_id=<ID>&baseline_branch=main" > $SP/ev_A.json
```

5. Run the driver from inside the action directory (it imports `./policy.ts`):

```bash
cp docs/superpowers/specs/r7-demo/r7demo_driver.ts .github/actions/test-system-io-ai-triage/src/ && cd .github/actions/test-system-io-ai-triage && SP=$SP node --experimental-strip-types src/r7demo_driver.ts
```

Delete the driver from `src/` afterwards — it is not part of the action.

## The one thing to be honest about

`MODEL` in the driver holds verdicts produced by **Opus 5 reading the evidence
packs and applying `agent.ts`'s rule table by hand**, because no
`ANTHROPIC_API_KEY` is available in this environment, so `agent.ts` cannot call
`api.anthropic.com`. The verdicts are a frontier model's, not a 31B's — but they
are not blind (the scenarios were authored here), and n=6.

So this demo establishes the **mechanism**, not model accuracy. The
model-independent results — the two arithmetic faults, the rate-shift gate, the
ledger, and the raw-pass-rate guarantee — do not depend on who produced the
verdicts. A blind accuracy number still needs production traffic.

## Master-side endpoints used for goal 2

```bash
curl -s "http://localhost:8099/api/v1/triage/pass-rates?repo=mattermost/mattermost&branch=main&window=30d"
```

```bash
curl -s "http://localhost:8099/api/v1/triage/alerts/evaluation?repo=mattermost/mattermost&branch=main"
```

The stabilization queue and the ledger need a key
(`go run ./cmd/tsioctl keys issue --name r7demo`), passed as `X-API-Key`:

```bash
curl -s -H "X-API-Key: $KEY" "http://localhost:8099/api/v1/triage/stabilization/queue?repo=mattermost/mattermost&branch=main"
```
