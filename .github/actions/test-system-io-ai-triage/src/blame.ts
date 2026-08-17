import type { HistorySummary } from "./types.ts";

export const MAX_NAMEABLE_RANGE = 8;

export interface SuspectCommit {
  sha: string;
  author: string | null;
  message: string;
}

/** GitHub compare-commits payload, enough to drop merges and name an author. */
export interface CompareCommit {
  sha?: string;
  parents?: unknown[];
  author?: { login?: string } | null;
  commit?: { author?: { name?: string } | null; message?: string };
}

export interface Blame {
  kind: "flaky" | "bug" | "unknown";
  resolvable: boolean;
  confident: boolean;
  reason: string;
  last_pass_commit?: string;
  failing_since_commit?: string;
  suspect?: SuspectCommit;
  candidates: SuspectCommit[];
}

export function kindOf(verdict: string): Blame["kind"] {
  if (verdict.startsWith("FLAKY_")) return "flaky";
  if (!verdict || verdict === "INCONCLUSIVE") return "unknown";
  return "bug";
}

export function resolveSuspectRange(history?: HistorySummary): {
  resolvable: boolean;
  reason: string;
  lastPass?: string;
  failingSince?: string;
} {
  if (!history) return { resolvable: false, reason: "no history for this test" };
  const lastPass = history.last_pass_commit;
  const failingSince = history.failing_since_commit;
  if (!failingSince) {
    return { resolvable: false, reason: "the test is not in a failing streak on the baseline" };
  }
  if (!lastPass) {
    return {
      resolvable: false,
      reason: "the test has not passed within the history window — not a fresh regression",
      failingSince,
    };
  }
  return {
    resolvable: true,
    reason: "suspect range is last pass … first fail",
    lastPass,
    failingSince,
  };
}

export function attribute(
  compareCommits: CompareCommit[],
  maxRange = MAX_NAMEABLE_RANGE,
): { confident: boolean; reason: string; commits: SuspectCommit[] } {
  const commits = (compareCommits || [])
    .filter((c) => !c.parents || c.parents.length <= 1)
    .map((c) => ({
      sha: c.sha || "",
      author: c.author?.login || c.commit?.author?.name || null,
      message: (c.commit?.message || "").split("\n")[0]!.slice(0, 120),
    }))
    .filter((c) => c.sha);

  if (commits.length === 0) {
    return { confident: false, reason: "no non-merge commits in the suspect range", commits: [] };
  }
  if (commits.length === 1) {
    return {
      confident: true,
      reason: "exactly one commit landed between the last pass and the first failure",
      commits,
    };
  }
  if (commits.length > maxRange) {
    return {
      confident: false,
      reason: `${commits.length} commits in the suspect range — too wide to name a culprit`,
      commits: commits.slice(0, maxRange),
    };
  }
  return {
    confident: false,
    reason: `${commits.length} candidate commits — needs a human to narrow`,
    commits,
  };
}

export function finishBlame(args: {
  verdict: string;
  history?: HistorySummary;
  attributed?: ReturnType<typeof attribute>;
  range?: ReturnType<typeof resolveSuspectRange>;
}): Blame {
  const kind = kindOf(args.verdict);
  if (kind !== "bug") {
    return {
      kind,
      resolvable: false,
      confident: false,
      reason: "not a bug — no author to name",
      candidates: [],
    };
  }
  const range = args.range ?? resolveSuspectRange(args.history);
  if (!range.resolvable) {
    return {
      kind,
      resolvable: false,
      confident: false,
      reason: range.reason,
      last_pass_commit: range.lastPass,
      failing_since_commit: range.failingSince,
      candidates: [],
    };
  }
  const attributed = args.attributed ?? {
    confident: false,
    reason: "compare not fetched",
    commits: [],
  };
  return {
    kind,
    resolvable: true,
    confident: attributed.confident,
    reason: attributed.reason,
    last_pass_commit: range.lastPass,
    failing_since_commit: range.failingSince,
    suspect: attributed.confident ? attributed.commits[0] : undefined,
    candidates: attributed.commits,
  };
}
