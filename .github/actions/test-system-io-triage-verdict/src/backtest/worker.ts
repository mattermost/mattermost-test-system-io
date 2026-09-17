/**
 * Remote worker for `adjudicate-offline --dump-packs`.
 *
 * Runs where ANTHROPIC_API_KEY is available (a CI job). Reads packs.jsonl
 * (first line is a header with model/system/schema), sends every pack through
 * the production request builder, and appends {key, verdict} lines to
 * responses.jsonl. Resumable: keys already in the output are skipped.
 *
 *   ANTHROPIC_API_KEY=... npm run adjudicate-worker -- packs.jsonl responses.jsonl [--concurrency 4] [--limit N]
 */
import { appendFileSync } from "node:fs";
import { adjudicationRequest, parseAnswer, type Pack } from "../adjudicate";
import { AnthropicClient } from "../anthropic";
import { log, parseArgs, readJSONL } from "./lib";

interface Header {
  header: true;
  model: string;
}
interface PackLine {
  key: string;
  pack: Pack;
  evidence_ids: string[];
}

async function main(): Promise<void> {
  const { opts, positional } = parseArgs(process.argv.slice(2));
  const [packsPath, outPath] = positional;
  if (!packsPath || !outPath) throw new Error("usage: worker <packs.jsonl> <responses.jsonl>");
  const concurrency = Number(opts.concurrency ?? 4);
  const limit = Number(opts.limit ?? 0);
  const lines = readJSONL<Header | PackLine>(packsPath);
  const header = lines[0] as Header;
  if (!header?.header) throw new Error("packs file has no header line");
  const packs = lines.slice(1) as PackLine[];
  const done = new Set(readJSONL<{ key: string }>(outPath).map((r) => r.key));
  let todo = packs.filter((p) => !done.has(p.key));
  if (limit) todo = todo.slice(0, limit);
  log(`${packs.length} packs, ${done.size} done, ${todo.length} to run with ${header.model}`);
  const client = new AnthropicClient({
    apiKey: process.env.ANTHROPIC_API_KEY ?? "",
    maxRetries: 4,
    timeoutMs: 120000,
  });
  const stats = { calls: 0, in: 0, out: 0, cache_read: 0, errors: 0 };
  const queue = [...todo];
  const workers = Array.from({ length: Math.max(1, concurrency) }, async () => {
    for (;;) {
      const p = queue.shift();
      if (!p) return;
      try {
        const response = await client.createMessage(adjudicationRequest(header.model, p.pack));
        const verdict = {
          ...parseAnswer(response),
          usage: {
            in: response.usage.input_tokens,
            out: response.usage.output_tokens,
            cache_read: response.usage.cache_read_input_tokens ?? 0,
          },
        };
        appendFileSync(outPath, JSON.stringify({ key: p.key, verdict }) + "\n");
        stats.calls++;
        stats.in += verdict.usage.in;
        stats.out += verdict.usage.out;
        stats.cache_read += verdict.usage.cache_read;
        if (stats.calls % 25 === 0)
          log(
            `${stats.calls} done; in=${stats.in} out=${stats.out} cache_read=${stats.cache_read}`,
          );
      } catch (error) {
        stats.errors++;
        log(`${p.key.slice(0, 8)}: ${String(error).slice(0, 600)}`);
      }
    }
  });
  await Promise.all(workers);
  log(JSON.stringify(stats));
}

main().catch((error) => {
  log(String(error));
  process.exit(1);
});
