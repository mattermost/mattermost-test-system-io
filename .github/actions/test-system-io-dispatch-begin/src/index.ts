import * as core from "@actions/core";
import { run } from "./main";

run().catch((err: unknown) => {
  // Top-level safety net: anything thrown from `run` that wasn't already
  // routed through core.setFailed lands here. Keep setFailed concise — it
  // surfaces in public CI logs. The stack goes to core.debug, only visible
  // with ACTIONS_STEP_DEBUG=true.
  const message = err instanceof Error ? err.message : String(err);
  if (err instanceof Error && err.stack) {
    core.debug(err.stack);
  }
  core.setFailed(`dispatch-begin action crashed: ${message}`);
});
