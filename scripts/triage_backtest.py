#!/usr/bin/env python3
"""Backtest the triage engine against historical PR runs.

For every completed PR report group in a TSIO instance (normally one seeded
from production with `tsioctl db import-remote`), this script:

  1. pulls ground truth from GitHub via `gh` (PR state, label events, later
     runs of the same PR, merge-base and changed files);
  2. asks TSIO for an admin-only `as_of` replay verdict, so the engine only
     sees the trunk evidence that existed when the run finished;
  3. classifies each run into a ground-truth bucket and reports agreement.

Ground-truth buckets (per PR run):
  GREEN            no failed tests; the engine must say SUCCESS
  WAIVED           a human applied the repo's "verified flaky" label while this
                   run was the PR's latest (E2E/Verified or "E2E Tests/verified")
  OVERRIDDEN       E2E/Override (blanket skip) applied; not evidence either way
  FIXED_BY_AUTHOR  failed, no waiver, and a later run on a *different* commit of
                   the same PR passed -> most likely a real regression
  RERUN_PASSED     failed, then a later run on the *same* commit passed -> flake
  UNRESOLVED       failed and no later run (PR abandoned / still open)

Usage:
  TSIO_API_KEY=... TSIO_ADMIN_KEY=... scripts/triage_backtest.py \
      --base-url http://localhost:8080/api/v1 --repository mattermost/mattermost-mobile \
      --since 2026-08-01T00:00:00Z --out backtest-mobile.md
"""
import argparse, json, os, subprocess, sys, urllib.request, urllib.error, datetime as dt, collections, functools, pathlib

VERIFIED_LABELS = {"mattermost/mattermost-mobile": ["E2E/Verified"], "mattermost/mattermost": ["E2E Tests/verified"]}
OVERRIDE_LABELS = ["E2E/Override"]
BASE_REF = {"mattermost/mattermost-mobile": "main", "mattermost/mattermost": "master"}

def parse_ts(s):
    return dt.datetime.fromisoformat(s.replace("Z", "+00:00"))

def api(base, path, method="GET", body=None, headers=None):
    req = urllib.request.Request(base + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json", **(headers or {})})
    with urllib.request.urlopen(req, timeout=300) as r:
        return r.status, json.load(r)

@functools.lru_cache(maxsize=None)
def gh(path, paginate=False):
    out = subprocess.run(["gh", "api"] + (["--paginate"] if paginate else []) + [path], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    # --paginate concatenates JSON documents for list endpoints
    text = out.stdout.strip()
    if text.startswith("["):
        items = []
        dec = json.JSONDecoder(); i = 0
        while i < len(text):
            obj, j = dec.raw_decode(text, i); items += obj; i = j
            while i < len(text) and text[i] in " \n\r\t": i += 1
        return items
    return json.loads(text)

def context_for(repo, name):
    if repo == "mattermost/mattermost-mobile":
        for prefix in ("mobile-pr-", "mobile-main-"):
            if name.startswith(prefix):
                return "e2e-test/" + name[len(prefix):]
        return "e2e-test/" + name
    lane = name.rsplit("-", 1)[-1]
    return "e2e-test/" + name[: -len(lane) - 1] + "/" + lane

def lane_for(repo, name):
    """Mirror of identity.InferLane in the server."""
    for marker in ("-release-cut", "-release", "-master", "-main"):
        if name.endswith(marker):
            name = name[: -len(marker)]
    for token in ("detox-ios", "detox-android", "detox-ipad", "maestro-ios", "maestro-android", "enterprise", "fips"):
        i = name.find("-" + token)
        if i < 0:
            continue
        rest = name[i + 1 + len(token):]
        if rest and not rest.startswith("-"):
            continue
        if rest.endswith("-e2e"):
            rest = rest[:-4]
        return token.replace("detox-", "").replace("maestro-", "") + rest
    return ""

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base-url", default="http://localhost:8080/api/v1")
    ap.add_argument("--repository", required=True)
    ap.add_argument("--since", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--cache", default=".triage-backtest-cache.json")
    ap.add_argument("--limit", type=int, default=0)
    a = ap.parse_args()
    key, admin = os.environ.get("TSIO_API_KEY"), os.environ.get("TSIO_ADMIN_KEY")
    if not key or not admin:
        sys.exit("TSIO_API_KEY and TSIO_ADMIN_KEY are required")
    since = parse_ts(a.since)
    cache = json.load(open(a.cache)) if os.path.exists(a.cache) else {}
    def cached(k, fn):
        if k not in cache:
            cache[k] = fn(); json.dump(cache, open(a.cache, "w"))
        return cache[k]

    # 1. all completed groups for the repo (PR and trunk) newest-first
    groups = []
    for off in range(0, 100000, 200):
        _, page = api(a.base_url, f"/reports?limit=200&offset={off}")
        rows = page["reports"]
        for g in rows:
            if g["repository"] == a.repository and g["status"] == "completed" and parse_ts(g["created_at"]) >= since:
                groups.append(g)
        if not rows or off + 200 >= page["total"] or (rows and parse_ts(rows[-1]["created_at"]) < since):
            break
    pr_groups = [g for g in groups if g.get("gh_pr_number")]
    pr_groups.sort(key=lambda g: g["created_at"])
    by_pr = collections.defaultdict(list)
    for g in pr_groups:  # sibling history must come from the full list, before any --limit
        by_pr[(g["gh_pr_number"], g["name"])].append(g)
    if a.limit:
        pr_groups = pr_groups[-a.limit:]
    print(f"{len(groups)} groups, {len(pr_groups)} PR runs across {len({g['gh_pr_number'] for g in pr_groups})} PRs", file=sys.stderr)

    # Pass 1: verdicts. "failed" for ground truth means failed *after* retries and
    # orchestration retests (the engine's terminal-failure count), not the raw
    # shard count in test_stats, which counts a failure that a retest recovered.
    verdicts = {}
    for idx, g in enumerate(pr_groups, 1):
        pr = g["gh_pr_number"]; sha = g["commit"]; repo = a.repository
        cmp = cached(f"cmp:{repo}:{sha}", lambda: gh(f"repos/{repo}/compare/{BASE_REF[repo]}...{sha}") or {})
        base_sha = (cmp.get("merge_base_commit") or {}).get("sha", "")
        changed = [f["filename"] for f in (cmp.get("files") or [])]
        run_end = g.get("last_upload_at") or g["created_at"]
        as_of = (parse_ts(run_end) + dt.timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
        body = {"composite_identity": {"repository": repo, "commit_sha": sha, "gh_run_id": g["gh_run_id"], "gh_run_attempt": g.get("gh_run_attempt") or "1", "name": g["name"]},
                "context": context_for(repo, g["name"]), "base_ref": BASE_REF[repo], "base_sha": base_sha, "lane": lane_for(repo, g["name"]),
                "changed_files": changed, "as_of": as_of}
        try:
            status, v = api(a.base_url, "/triage/verdicts", "POST", body, {"X-API-Key": key, "X-Admin-Key": admin})
        except urllib.error.HTTPError as e:
            v = {"verdict": f"HTTP {e.code}", "findings": [], "counts": {}, "confidence": 0}
            try: v["error"] = e.read().decode()[:200]
            except Exception: pass
        if v.get("verdict") == "ACTION_REQUIRED" and "No test observations" in (v.get("reason") or "") and (g.get("test_stats") or {}).get("failed", 0) == 0:
            # Metadata-only import of a green production run: no cases to judge, green for gating.
            v = {"verdict": "SUCCESS", "findings": [], "counts": {"failed": 0, "passed": (g.get("test_stats") or {}).get("passed", 0)}, "confidence": 1, "metadata_only": True}
        verdicts[g["id"]] = (v, base_sha, changed, as_of, run_end)
        print(f"[{idx}/{len(pr_groups)}] verdict PR {pr} {g['name']} {sha[:7]} raw_failed={(g.get('test_stats') or {}).get('failed', 0)} effective_failed={v.get('counts', {}).get('failed')} {v.get('verdict')}", file=sys.stderr)
    def effective_failed(g):
        v = verdicts.get(g["id"], (None,))[0]
        c = (v or {}).get("counts") or {}
        return c["failed"] if "failed" in c else (g.get("test_stats") or {}).get("failed", 0)

    # Pass 2: ground truth and report rows.
    rows = []
    for idx, g in enumerate(pr_groups, 1):
        pr = g["gh_pr_number"]; sha = g["commit"]; repo = a.repository
        stats = g.get("test_stats") or {}
        failed = effective_failed(g)
        v, base_sha, changed, as_of, run_end = verdicts[g["id"]]
        info = cached(f"pr:{repo}:{pr}", lambda: gh(f"repos/{repo}/pulls/{pr}") or {})
        events = cached(f"events:{repo}:{pr}", lambda: [e for e in (gh(f"repos/{repo}/issues/{pr}/events?per_page=100", True) or []) if e.get("event") == "labeled"])
        # later runs of the same PR + lane
        siblings = by_pr[(pr, g["name"])]
        later = [s for s in siblings if s["created_at"] > g["created_at"]]
        next_at = later[0]["created_at"] if later else None
        def label_in_window(names):
            for e in events:
                if e.get("label", {}).get("name") in names and e["created_at"] >= run_end and (next_at is None or e["created_at"] < next_at):
                    return e["created_at"]
            return None
        waived = label_in_window(VERIFIED_LABELS[repo]); overridden = label_in_window(OVERRIDE_LABELS)
        later_same = [s for s in later if s["commit"] == sha and effective_failed(s) == 0]
        later_other = [s for s in later if s["commit"] != sha and effective_failed(s) == 0]
        if failed == 0:
            truth = "GREEN"
        elif waived:
            truth = "WAIVED"
        elif overridden:
            truth = "OVERRIDDEN"
        elif later_same:
            truth = "RERUN_PASSED"
        elif later_other:
            truth = "FIXED_BY_AUTHOR"
        else:
            truth = "UNRESOLVED"
        classes = collections.Counter(f["class"] for f in v.get("findings", []))
        verdict = v.get("verdict")
        would_unblock = verdict in ("SUCCESS", "NEUTRAL")
        if truth == "GREEN": agree = would_unblock
        elif truth in ("WAIVED", "RERUN_PASSED"): agree = would_unblock
        elif truth == "FIXED_BY_AUTHOR": agree = not would_unblock
        else: agree = None
        findings = [{"class": f.get("class"), "title": (f.get("full_title") or "")[:120], "file": f.get("file"), "reason": (f.get("reason") or "")[:160],
                     "trunk": {k: f.get("trunk", {}).get(k) for k in ("runs", "fails", "flaky", "consecutive_fails")}, "excerpt": (f.get("pr", {}).get("error_excerpt") or "")[:120],
                     "match": f.get("pr", {}).get("signature_match")} for f in v.get("findings", [])]
        rows.append({"repo": repo, "pr": pr, "name": g["name"], "sha": sha[:7], "run_at": g["created_at"], "state": info.get("state"), "merged": bool(info.get("merged_at")),
                     "failed": failed, "raw_failed": stats.get("failed", 0), "flaky": stats.get("flaky", 0), "truth": truth, "verdict": verdict, "confidence": v.get("confidence"),
                     "classes": dict(classes), "counts": v.get("counts", {}), "agree": agree, "error": v.get("error"), "as_of": as_of, "base_sha": base_sha[:7], "changed": len(changed),
                     "waived_at": waived, "findings": findings})
        print(f"[{idx}/{len(pr_groups)}] PR {pr} {g['name']} {sha[:7]} failed={failed} truth={truth} verdict={verdict} {dict(classes)}", file=sys.stderr)
    # A run whose effective failures are zero but whose raw shard count was not
    # passed only thanks to retries/retests; it is GREEN for gating purposes.

    # 2. report
    lines = [f"# Triage backtest: {a.repository} since {a.since}", "", f"Runs: {len(rows)} PR runs, {len({r['pr'] for r in rows})} PRs. Verdicts computed with `as_of` = run end + 5 min.", ""]
    summary = collections.Counter((r["truth"], r["verdict"]) for r in rows)
    lines += ["## Ground truth vs verdict", "", "| Ground truth | Verdict | Runs |", "|---|---|---|"]
    for (t, vd), n in sorted(summary.items()):
        lines.append(f"| {t} | {vd} | {n} |")
    scored = [r for r in rows if r["agree"] is not None and r["truth"] != "GREEN"]
    if scored:
        ok = sum(1 for r in scored if r["agree"])
        lines += ["", f"Agreement on scoreable failing runs (WAIVED/RERUN_PASSED should unblock, FIXED_BY_AUTHOR should block): {ok}/{len(scored)}", ""]
    fe = [r for r in rows if r["truth"] == "FIXED_BY_AUTHOR" and r["agree"] is False]
    lines += ["", f"False exonerations (author fixed it, engine would have unblocked): {len(fe)}", ""]
    lines += ["## Failing runs", "", "| Repo | PR | Lane | SHA | Run at | Failed | Ground truth | Verdict | Classes | Would unblock |", "|---|---|---|---|---|---|---|---|---|---|"]
    for r in sorted(rows, key=lambda r: (r["truth"], r["run_at"])):
        if r["truth"] == "GREEN": continue
        cls = ", ".join(f"{k}×{v}" for k, v in sorted(r["classes"].items()))
        lines.append(f"| {r['repo'].split('/')[-1]} | [{r['pr']}](https://github.com/{r['repo']}/pull/{r['pr']}) | {r['name']} | {r['sha']} | {r['run_at'][:16]} | {r['failed']} | {r['truth']} | {r['verdict']} | {cls} | {'yes' if r['verdict'] in ('SUCCESS','NEUTRAL') else 'no'} |")
    green = [r for r in rows if r["truth"] == "GREEN"]
    bad_green = [r for r in green if r["verdict"] not in ("SUCCESS", "NEUTRAL")]
    lines += ["", f"Green runs: {len(green)}; engine disagreed on {len(bad_green)}: " + ", ".join(f"PR {r['pr']} {r['name']} → {r['verdict']}" for r in bad_green[:20]), ""]
    pathlib.Path(a.out).write_text("\n".join(lines))
    pathlib.Path(a.out).with_suffix(".json").write_text(json.dumps(rows, indent=1))
    print(f"wrote {a.out}", file=sys.stderr)

if __name__ == "__main__":
    main()
