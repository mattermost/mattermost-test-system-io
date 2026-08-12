export interface ReportURLIdentity {
  repository: string;
  commit_sha: string;
  gh_run_id: string;
  gh_run_attempt?: string;
  name: string;
  branch?: string;
}

/** Match apps/web encodeBranchPathSegment: strip refs/* and map `/` → `~`. */
export function encodeBranchPathSegment(branch: string): string {
  return (branch || "main")
    .replace(/^refs\/heads\//, "")
    .replace(/^refs\/tags\//, "")
    .replace(/\//g, "~");
}

export function buildReportURL(baseURL: string, c: ReportURLIdentity): string {
  const repoTrailing = (c.repository || "").split("/").pop() || c.repository;
  const repo = encodeURIComponent(repoTrailing);
  const branch = encodeURIComponent(encodeBranchPathSegment(c.branch || "main"));
  const shortSha = (c.commit_sha || "").slice(0, 7);
  const name = encodeURIComponent(c.name);
  const attempt = encodeURIComponent(c.gh_run_attempt || "1");
  return (
    `${baseURL}/reports/${repo}/${branch}/${shortSha}/${name}` +
    `?gh_run_id=${encodeURIComponent(c.gh_run_id)}&gh_run_attempt=${attempt}`
  );
}
