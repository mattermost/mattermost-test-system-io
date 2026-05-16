import * as core from "@actions/core";
import { run } from "./main";

run().catch((err: unknown) => {
  // Top-level safety net: anything thrown from `run` that wasn't already
  // routed through core.setFailed lands here. Re-fail with a useful
  // message so the workflow step exits non-zero.
  const message = err instanceof Error ? (err.stack ?? err.message) : String(err);
  core.setFailed(`dispatch-begin action crashed: ${message}`);
});
