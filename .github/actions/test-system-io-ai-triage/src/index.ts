import * as core from "@actions/core";

import { run } from "./main.ts";
import { runReplay } from "./replay.ts";

// Two tasks share this action so that they share the policy layer. "triage" is
// the live path a CI run takes; "replay" re-adjudicates already-ingested runs
// to produce an accuracy number before any calling workflow is merged. An
// unrecognised task is a configuration error, not a reason to silently triage.
const task = (core.getInput("task") || "triage").toLowerCase();

const entry =
  task === "replay"
    ? runReplay()
    : task === "triage"
      ? run()
      : Promise.reject(new Error(`unknown task ${JSON.stringify(task)} — expected triage or replay`));

entry.catch((err: unknown) => {
  const message = err instanceof Error ? (err.stack ?? err.message) : String(err);
  core.setFailed(`ai-triage action crashed: ${message}`);
});
