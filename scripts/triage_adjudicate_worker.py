#!/usr/bin/env python3
"""Remote worker for scripts/triage_adjudicate.py --dump-packs.

Runs where ANTHROPIC_API_KEY is available (a CI job). Reads packs.jsonl (first
line is a header with model/system/schema), calls Claude for each pack with
structured JSON output, and appends {key, verdict} lines to responses.jsonl.
Resumable: keys already present in the output file are skipped.

  ANTHROPIC_API_KEY=... python3 triage_adjudicate_worker.py packs.jsonl responses.jsonl [--concurrency 4]
"""
import json, sys, threading, queue, argparse, time
import anthropic

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("packs"); ap.add_argument("out")
    ap.add_argument("--concurrency", type=int, default=4)
    ap.add_argument("--limit", type=int, default=0)
    a = ap.parse_args()
    lines = [json.loads(l) for l in open(a.packs) if l.strip()]
    header, packs = lines[0], lines[1:]
    done = set()
    try:
        for l in open(a.out):
            if l.strip(): done.add(json.loads(l)["key"])
    except FileNotFoundError:
        pass
    todo = [p for p in packs if p["key"] not in done]
    if a.limit: todo = todo[: a.limit]
    print(f"{len(packs)} packs, {len(done)} done, {len(todo)} to run with {header['model']}", file=sys.stderr)
    client = anthropic.Anthropic(max_retries=4)
    lock, q, out = threading.Lock(), queue.Queue(), open(a.out, "a")
    for p in todo: q.put(p)
    stats = {"calls": 0, "in": 0, "out": 0, "cache_read": 0, "errors": 0}
    def work():
        while True:
            try: p = q.get_nowait()
            except queue.Empty: return
            user = "Evidence pack (JSON). Valid evidence ids to cite: " + ", ".join(p["evidence_ids"]) + "\n\n" + json.dumps(p["pack"], indent=1)
            try:
                resp = client.messages.create(
                    model=header["model"], max_tokens=2000,
                    system=[{"type": "text", "text": header["system"], "cache_control": {"type": "ephemeral"}}],
                    messages=[{"role": "user", "content": user}],
                    # Haiku 4.5 has no effort control; every current Opus/Sonnet model does.
                    output_config={"format": {"type": "json_schema", "schema": header["schema"]}, **({} if header["model"].startswith("claude-haiku") else {"effort": "medium"})},
                )
                if resp.stop_reason not in ("end_turn", "stop_sequence"):
                    raise RuntimeError(f"stop_reason={resp.stop_reason}")
                verdict = json.loads(next(b.text for b in resp.content if b.type == "text"))
                verdict["usage"] = {"in": resp.usage.input_tokens, "out": resp.usage.output_tokens, "cache_read": resp.usage.cache_read_input_tokens or 0}
                with lock:
                    out.write(json.dumps({"key": p["key"], "verdict": verdict}) + "\n"); out.flush()
                    stats["calls"] += 1; stats["in"] += resp.usage.input_tokens; stats["out"] += resp.usage.output_tokens; stats["cache_read"] += resp.usage.cache_read_input_tokens or 0
                    if stats["calls"] % 25 == 0: print(f"{stats['calls']} done; in={stats['in']:,} out={stats['out']:,} cache_read={stats['cache_read']:,}", file=sys.stderr)
            except (anthropic.RateLimitError, anthropic.APIConnectionError) as e:
                with lock: stats["errors"] += 1
                time.sleep(10); q.put(p)
            except anthropic.APIStatusError as e:
                with lock: stats["errors"] += 1
                print(f"{p['key'][:8]}: {e.status_code} {str(e)[:600]}", file=sys.stderr)
            except Exception as e:  # noqa: BLE001 - keep the batch going, report at the end
                with lock: stats["errors"] += 1
                print(f"{p['key'][:8]}: {type(e).__name__} {str(e)[:120]}", file=sys.stderr)
    threads = [threading.Thread(target=work) for _ in range(a.concurrency)]
    for t in threads: t.start()
    for t in threads: t.join()
    print(json.dumps(stats), file=sys.stderr)

if __name__ == "__main__":
    main()
