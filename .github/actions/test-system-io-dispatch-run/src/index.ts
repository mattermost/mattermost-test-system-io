import * as core from "@actions/core";
import { run } from "./main";

run().catch((err: unknown) => {
  // Keep setFailed concise — its output lands in public CI logs. The full
  // stack goes to core.debug, which only shows when ACTIONS_STEP_DEBUG=true.
  const message = err instanceof Error ? err.message : String(err);
  if (err instanceof Error && err.stack) {
    core.debug(err.stack);
  }
  core.setFailed(`dispatch-run action crashed: ${message}`);
});
