import * as core from "@actions/core";
import { run } from "./main";

run().catch((err: unknown) => {
  const message = err instanceof Error ? (err.stack ?? err.message) : String(err);
  core.setFailed(`summary action crashed: ${message}`);
});
