/**
 * A worker that never leased a unit ran no tests, so it has no report to
 * upload: it started after the queue drained (409 RUN_NOT_IN_PROGRESS on its
 * first checkout) or gave up polling an empty queue. Without telling the
 * orchestrator, the run's report group waits for its report forever and is
 * reaped to `incomplete`, even when every test passed.
 *
 * Kept free of runtime imports from sibling modules so `node --test` can load
 * it directly; main.ts passes in its postJSON.
 */

import * as core from "@actions/core";

export type PostJSONFn = (
  urlPath: string,
  body: Record<string, unknown>,
) => Promise<{ status: number; body: unknown }>;

/**
 * Only a worker that drained cleanly without a single lease may say it had
 * no work. One that failed talking to the orchestrator may have missed work
 * it should have run, and one that leased units owes its report: either way
 * the missing report must keep the group short.
 */
export function ranNoWork(leasesHeld: number, drainErr: Error | undefined): boolean {
  return leasesHeld === 0 && drainErr === undefined;
}

/**
 * Records this worker as having left without work. Failure only warns: the
 * group then stays short of total_reports_expected, as it did before this
 * call existed, and the warning says why.
 */
export async function recordNoWork(post: PostJSONFn, body: Record<string, unknown>): Promise<void> {
  const res = await post("/api/v1/orchestration/no-work", body);
  // Check the body too: a server without this route answers an unknown path
  // with the web UI's index.html, which can be a 200.
  const recorded = (res.body as { recorded?: unknown } | null)?.recorded === true;
  if (res.status === 200 && recorded) {
    core.info(
      "no unit leased; recorded no-work exit so the report group does not wait for this worker",
    );
    return;
  }
  core.warning(
    `no-work record failed: ${res.status} ${JSON.stringify(res.body)}; ` +
      "the report group will stay short of total_reports_expected and read as missing reports",
  );
}
