#!/usr/bin/env python3
"""Adjudicate borderline triage findings with Claude and score the result offline.

Second judge in the triage design: the deterministic engine decides the clear
cases; for borderline findings this script builds an evidence pack (full error,
trunk + cross-PR history, the PR's changed files and the diff hunks of files
named in the error) and asks Claude for a structured cause. The decision matrix:

  engine certain (REGRESSION on a spec the PR edited, NEW_TEST, INFRA)   -> block, no call
  engine exonerated (BROKEN_ON_TRUNK, FLAKY_CONFIRMED, FLAKY_CROSS_PR)    -> unblock; Claude may VETO
                                                                            (caused_by_pr >= 0.9 with a cited diff hunk)
  borderline (REGRESSION, REGRESSION_CLUSTER/AREA, OWNED_BY_PR via helper,
              INSUFFICIENT_DATA, FLAKY_SUSPICIOUS)                          -> unblock only if Claude says
                                                                            flaky_environment / bug_on_master with
                                                                            confidence >= 0.85 AND cites checkable evidence

Inputs: a backtest JSON (from scripts/triage_backtest.py, optionally refined),
the local TSIO Postgres (docker container publishing :6432) and the gh compare
cache written by the backtest. Responses are cached by evidence hash so reruns
and re-scoring are free. `--dry-run` builds packs and prints token estimates
without calling the API.

Usage:
  ANTHROPIC_API_KEY=... scripts/triage_adjudicate.py --backtest backtest-mobile.refined.json \
      --gh-cache gh-cache.json --out adjudicated-mobile.json [--dry-run] [--model claude-sonnet-5]
"""
import argparse, collections, datetime as dt, hashlib, json, os, pathlib, subprocess, sys

BORDERLINE = {"REGRESSION", "REGRESSION_CLUSTER", "REGRESSION_AREA", "OWNED_BY_PR", "INSUFFICIENT_DATA", "FLAKY_SUSPICIOUS"}
EXONERATED = {"BROKEN_ON_TRUNK", "FLAKY_CONFIRMED", "FLAKY_CROSS_PR", "QUARANTINED"}
CERTAIN = {"NEW_TEST", "INFRA"}
UNBLOCK_MIN, VETO_MIN, MAX_FINDINGS_PER_RUN = 0.85, 0.90, 8

SYSTEM = """You are the second judge in an automated CI triage system for end-to-end test failures on pull requests.
A deterministic engine has already classified each failing test from statistics (trunk history, other PRs' history,
file ownership). You adjudicate ONE finding at a time using the evidence pack and answer a single question:
what caused this test to fail on this PR run?

Causes:
- caused_by_pr: the PR's code change plausibly produces this failure (the error is about behavior, selectors, data or
  flows the diff touches; or the failing test/helper is modified by the PR).
- flaky_environment: the failure is environmental or timing-related and unrelated to the diff (server/API health,
  device or emulator problems, timeouts waiting for UI that the PR does not touch, the same failure recurring on
  unrelated PRs). This includes failures that also occurred on other PRs the author had nothing to do with.
- bug_on_master: the same failure already occurs on the trunk branch (master/main) before this PR, so the PR inherits it.
- test_bug: the test itself is wrong or brittle in a way the PR did not introduce (stale assertion, race in the test).

Rules:
- Cite only evidence items by their id from the pack. Never invent PR numbers, files or errors.
- caused_by_pr requires a concrete link between the error and the diff: name the changed file or hunk that explains it.
  A PR touching many files is not by itself evidence; a PR touching the failing spec, a helper in the stack, or the
  feature under test is.
- flaky_environment with high confidence requires either recurrence on other PRs (evidence id starting with cross_pr)
  or an error text that is clearly infrastructural (server not healthy, cannot connect, device/emulator failure, app crash on
  launch) together with a diff that does not touch that area.
- If the evidence is genuinely insufficient, answer with confidence below 0.6 rather than guessing.
- Be precise and terse in the explanation: one paragraph a developer can act on."""

SCHEMA = {
    "type": "object",
    "properties": {
        "cause": {"type": "string", "enum": ["caused_by_pr", "flaky_environment", "bug_on_master", "test_bug"]},
        "confidence": {"type": "number", "description": "0 to 1"},
        "cited_evidence": {"type": "array", "items": {"type": "string"}, "description": "up to 6 evidence ids from the pack"},
        "explanation": {"type": "string", "description": "one short paragraph a developer can act on"},
    },
    "required": ["cause", "confidence", "cited_evidence", "explanation"],
    "additionalProperties": False,
}

def psql(sql):
    cid = subprocess.run(["docker", "ps", "-qf", "publish=6432"], capture_output=True, text=True).stdout.split()[0]
    out = subprocess.run(["docker", "exec", cid, "psql", "-U", "tsio", "-d", "tsio", "-tAc", sql], capture_output=True, text=True)
    return out.stdout.strip()

def q(s):  # dollar-quote for psql
    return "$q$" + s.replace("$", "") + "$q$"

def group_id(r):
    return psql(f"select id from report_groups where repository={q(r['repo'])} and name={q(r['name'])} and commit_sha like {q(r['sha'] + '%')} order by created_at desc limit 1")

def full_error(gid, title_prefix):
    row = psql(f"""select coalesce(c.error_message,'') from test_cases c join test_identities i on i.id=c.identity_id
      join suites s on s.id=c.suite_id join reports rp on rp.id=s.report_id
      where rp.report_group_id='{gid}' and left(i.normalized_title,{len(title_prefix)})={q(title_prefix)} and c.status in ('failed','timedOut','interrupted')
      order by c.ordinal limit 1""")
    return row[:2500]

def cross_pr(r, title_prefix, as_of):
    since = (dt.datetime.fromisoformat(as_of.replace("Z", "+00:00")) - dt.timedelta(days=14)).strftime("%Y-%m-%d %H:%M:%S")
    until = as_of.replace("T", " ").replace("Z", "")
    lane = psql(f"select coalesce(environment_metadata->>'lane','') from report_groups where repository={q(r['repo'])} and name={q(r['name'])} limit 1")
    rows = psql(f"""select o.gh_pr_number, o.status, left(o.commit_sha,7), to_char(o.observed_at,'MM-DD') from test_observations o join test_identities i on i.id=o.identity_id
      where i.repository={q(r['repo'])} and o.lane={q(lane)} and o.branch_kind='pr' and o.gh_pr_number<>{r['pr']}
      and left(i.normalized_title,{len(title_prefix)})={q(title_prefix)} and o.observed_at between '{since}' and '{until}' and not o.is_infra_stub
      order by o.observed_at desc""")
    fails, passes = [], 0
    for line in rows.splitlines():
        pr, status, sha, day = line.split("|")
        if status in ("failed", "timedOut", "interrupted"):
            fails.append(f"PR {pr} ({sha}, {day})")
        elif status == "passed":
            passes += 1
    return fails, passes

def diff_evidence(cmp, error_text, test_file):
    """Changed file names plus diff hunks: files named in the error or stack, and
    (for small PRs, <= 8 files) every file, so the judge can see the whole change."""
    files = cmp.get("files") or []
    names = [f["filename"] for f in files]
    hunks = []
    lowered = error_text.lower()
    small = len(files) <= 8
    for f in files:
        base = f["filename"].rsplit("/", 1)[-1]
        stem = base.split(".")[0]
        named = f["filename"] == test_file or (len(stem) > 3 and stem.lower() in lowered) or base in error_text
        if (named or small) and f.get("patch"):
            hunks.append({"file": f["filename"], "patch": f["patch"][:4000 if named else 2500]})
        if len(hunks) >= 8:
            break
    return names[:200], hunks

def build_pack(r, f, cmp, idx):
    gid = group_id(r)
    title = f["title"]
    err = full_error(gid, title) if gid else f.get("excerpt", "")
    fails, passes = cross_pr(r, title, r["as_of"])
    changed, hunks = diff_evidence(cmp, err + "\n" + f.get("file", ""), f.get("file", ""))
    ev = collections.OrderedDict()
    ev["test"] = {"title": title, "file": f["file"], "lane": r["name"]}
    ev["error"] = err or f.get("excerpt", "")
    ev["engine"] = {"class": f["class"], "reason": f["reason"], "signature_matches_trunk_dominant_failure": f.get("match")}
    t = f.get("trunk") or {}
    ev["trunk_history_14d"] = {"runs": t.get("runs"), "fails": t.get("fails"), "flaky": t.get("flaky"), "consecutive_fails_at_head": t.get("consecutive_fails")}
    ev["cross_pr_failures_14d"] = {"id": "cross_pr", "other_prs_where_this_test_failed": fails[:12], "other_pr_runs_where_it_passed": passes}
    ev["pr"] = {"number": r["pr"], "repository": r["repo"], "title": r.get("pr_title", ""), "changed_file_count": r.get("changed"), "changed_files": changed,
                "spec_file_changed_by_pr": f["file"] in changed}
    ev["diff_hunks_of_files_named_in_error"] = [{"id": f"hunk_{i}", **h} for i, h in enumerate(hunks)]
    ev["other_failures_in_same_run"] = idx
    return ev

def evidence_ids(ev):
    ids = ["test", "error", "engine", "trunk_history_14d", "cross_pr", "pr.changed_files"]
    ids += [h["id"] for h in ev["diff_hunks_of_files_named_in_error"]]
    return ids

def decide(cls, verdict):
    """Apply the decision matrix. Returns (blocking: bool, decided_by)."""
    if verdict is None:
        return cls not in EXONERATED, "engine"
    cause, conf, cited = verdict["cause"], max(0.0, min(1.0, float(verdict["confidence"]))), verdict["cited_evidence"]
    cites_hunk = any(c.startswith("hunk_") for c in cited)
    cites_cross = "cross_pr" in cited
    if cls in EXONERATED:
        if cause == "caused_by_pr" and conf >= VETO_MIN and cites_hunk:
            return True, "adjudicator_veto"
        return False, "engine"
    if cls in BORDERLINE:
        if cause in ("flaky_environment", "bug_on_master", "test_bug") and conf >= UNBLOCK_MIN and (cites_cross or cites_hunk or cause == "bug_on_master"):
            return False, "adjudicator_unblock"
        return True, "engine"
    return True, "engine"

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--backtest", required=True)
    ap.add_argument("--gh-cache", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--cache", default=".triage-adjudicate-cache.json")
    ap.add_argument("--model", default="claude-sonnet-5")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--buckets", default="LIKELY_REGRESSION,RECURRING_ELSEWHERE,WAIVED,RERUN_PASSED,FIXED_BY_AUTHOR")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--dump-packs", default="", help="Write evidence packs as JSONL (no API calls) for a remote worker")
    ap.add_argument("--import-responses", default="", help="JSONL of {key, verdict} produced by the remote worker; merged into the cache")
    a = ap.parse_args()
    if a.import_responses:
        cache = json.load(open(a.cache)) if os.path.exists(a.cache) else {}
        n = 0
        for line in open(a.import_responses):
            if line.strip():
                rec = json.loads(line); cache[rec["key"]] = rec["verdict"]; n += 1
        json.dump(cache, open(a.cache, "w"))
        print(f"imported {n} responses into {a.cache}", file=sys.stderr)
        a.dry_run = True  # score from the cache; anything missing stays engine-decided
    dump = open(a.dump_packs, "w") if a.dump_packs else None
    if dump:
        dump.write(json.dumps({"header": True, "model": a.model, "system": SYSTEM, "schema": SCHEMA}) + "\n")
        a.dry_run = True
    rows = json.load(open(a.backtest))
    gh = json.load(open(a.gh_cache))
    cache = json.load(open(a.cache)) if os.path.exists(a.cache) else {}
    buckets = set(a.buckets.split(","))
    client = None
    if not a.dry_run:
        import anthropic
        client = anthropic.Anthropic()

    dumped = set()
    def ask(pack):
        key = hashlib.sha256((a.model + json.dumps(pack, sort_keys=True)).encode()).hexdigest()
        if key in cache:
            return cache[key]
        if dump and key not in dumped:
            dumped.add(key)
            dump.write(json.dumps({"key": key, "pack": pack, "evidence_ids": evidence_ids(pack)}) + "\n")
        if a.dry_run:
            return None
        user = "Evidence pack (JSON). Valid evidence ids to cite: " + ", ".join(evidence_ids(pack)) + "\n\n" + json.dumps(pack, indent=1)
        resp = client.messages.create(
            model=a.model,
            max_tokens=2000,
            system=[{"type": "text", "text": SYSTEM, "cache_control": {"type": "ephemeral"}}],
            messages=[{"role": "user", "content": user}],
            output_config={"format": {"type": "json_schema", "schema": SCHEMA}, "effort": "medium"},
        )
        if resp.stop_reason not in ("end_turn", "stop_sequence"):
            raise RuntimeError(f"unexpected stop_reason {resp.stop_reason}")
        text = next(b.text for b in resp.content if b.type == "text")
        verdict = json.loads(text)
        verdict["usage"] = {"in": resp.usage.input_tokens, "out": resp.usage.output_tokens, "cache_read": resp.usage.cache_read_input_tokens}
        cache[key] = verdict
        json.dump(cache, open(a.cache, "w"))
        return verdict

    selected = [r for r in rows if r["truth"] in buckets and r["findings"]]
    if a.limit:
        selected = selected[: a.limit]
    print(f"{len(selected)} runs to adjudicate", file=sys.stderr)
    out, est_tokens, calls = [], 0, 0
    for n, r in enumerate(selected, 1):
        sha_full = next((k.split(":")[-1] for k in gh if k.startswith("cmp:") and k.split(":")[-1].startswith(r["sha"])), None)
        cmp = gh.get(f"cmp:{r['repo']}:{sha_full}", {}) if sha_full else {}
        r["pr_title"] = (gh.get(f"pr:{r['repo']}:{r['pr']}") or {}).get("title", "")
        idx = [{"class": x["class"], "title": x["title"][:80]} for x in r["findings"] if x is not None][:12]
        decisions, blocking = [], 0
        for f in r["findings"][:MAX_FINDINGS_PER_RUN]:
            cls = f["class"]
            if cls in CERTAIN or (cls == "OWNED_BY_PR" and f["file"] in (cmp.get("files") and [x["filename"] for x in cmp["files"]] or [])):
                decisions.append({"title": f["title"], "class": cls, "blocking": True, "by": "engine"}); blocking += 1; continue
            if cls not in BORDERLINE and cls not in EXONERATED:
                decisions.append({"title": f["title"], "class": cls, "blocking": True, "by": "engine"}); blocking += 1; continue
            pack = build_pack(r, f, cmp, idx)
            est_tokens += len(json.dumps(pack)) // 4 + 300
            v = ask(pack)
            if v is not None and "usage" in v: calls += 1
            b, by = decide(cls, v)
            blocking += b
            decisions.append({"title": f["title"], "class": cls, "blocking": b, "by": by, "adjudicator": v and {k: v[k] for k in ("cause", "confidence", "cited_evidence", "explanation")}})
        for f in r["findings"][MAX_FINDINGS_PER_RUN:]:
            blocking += f["class"] not in EXONERATED
        new_verdict = "FAILURE" if blocking else "SUCCESS"
        out.append({**{k: r[k] for k in ("repo", "pr", "name", "sha", "run_at", "truth", "verdict", "classes")}, "adjudicated_verdict": new_verdict, "decisions": decisions})
        print(f"[{n}/{len(selected)}] PR {r['pr']} {r['name']} {r['sha']} {r['truth']}: engine={r['verdict']} adjudicated={new_verdict}", file=sys.stderr)
    json.dump(out, open(a.out, "w"), indent=1)
    if dump:
        dump.close()
        print(f"wrote {len(dumped)} packs to {a.dump_packs}", file=sys.stderr)
    if a.dry_run and not a.import_responses:
        print(f"dry run: ~{est_tokens:,} input tokens across {sum(len(r['decisions']) for r in out)} findings ({len(out)} runs)", file=sys.stderr)
        return
    U = ("SUCCESS", "NEUTRAL")
    m = collections.Counter((r["truth"], r["verdict"] in U, r["adjudicated_verdict"] in U) for r in out)
    print("\n| Ground truth | runs | engine green | adjudicated green |\n|---|---|---|---|")
    for t in sorted({r["truth"] for r in out}):
        n = sum(v for (tt, _, _), v in m.items() if tt == t)
        print(f"| {t} | {n} | {sum(v for (tt, e, _), v in m.items() if tt == t and e)} | {sum(v for (tt, _, ad), v in m.items() if tt == t and ad)} |")
    print(f"\nAPI calls this run: {calls}")

if __name__ == "__main__":
    main()
