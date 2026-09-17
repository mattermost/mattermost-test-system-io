export interface Finding {
  file: string;
  full_title: string;
  class: string;
  blocking: boolean;
  reason: string;
  pr: { failure_locus: string };
  links?: { tsio_case_url?: string; tsio_history_url?: string };
}
export interface Verdict {
  id: string;
  verdict: string;
  mode: "off" | "shadow" | "enforce";
  confidence: number;
  counts: { blocking: number; exonerated: number; infra?: number };
  findings: Finding[];
  markdown: { check_summary: string; pr_comment: string; status_description: string };
  human_override?: { actor: string; label: string; resulting_state: string };
  adjudication?: { model: string; final_verdict: string; blocking: number; exonerated: number };
}
export function conclusion(verdict: string): "success" | "neutral" | "failure" | "action_required" {
  if (verdict === "SUCCESS") return "success";
  if (verdict === "NEUTRAL") return "neutral";
  if (verdict === "ACTION_REQUIRED" || verdict === "INCOMPLETE") return "action_required";
  return "failure";
}
export function statusState(verdict: string): "success" | "failure" {
  return verdict === "SUCCESS" || verdict === "NEUTRAL" ? "success" : "failure";
}
export function shouldPostStatus(mode: Verdict["mode"], input: string): boolean {
  if (!["auto", "true", "false"].includes(input))
    throw new Error("post-commit-status must be auto, true or false");
  // Shadow and off are policy-level guarantees, including with explicit true.
  return mode === "enforce" && input !== "false";
}
export function annotation(finding: Finding, baseURL = "https://test-io.test.mattermost.com") {
  const frame = finding.pr.failure_locus.match(/^(.*?):(\d+)(?::\d+)?$/);
  const line = Number(frame?.[2] || 1);
  const level: "warning" | "failure" | "notice" = ["INFRA", "INSUFFICIENT_DATA"].includes(
    finding.class,
  )
    ? "warning"
    : finding.blocking
      ? "failure"
      : "notice";
  return {
    path: frame?.[1] || finding.file,
    start_line: line,
    end_line: line,
    annotation_level: level,
    title: `${finding.class}: ${finding.full_title}`.slice(0, 255),
    message: [
      finding.reason,
      ...[finding.links?.tsio_case_url, finding.links?.tsio_history_url]
        .filter((link): link is string => Boolean(link))
        .map((link) => new URL(link, baseURL).href),
    ]
      .join("\n")
      .slice(0, 65000),
  };
}
export interface Comment {
  id: number;
  body?: string | null;
  user?: { type?: string } | null;
}
export interface CommentWriter {
  list(): Promise<Comment[]>;
  create(body: string): Promise<unknown>;
  update(id: number, body: string): Promise<unknown>;
}
export async function upsertComment(
  writer: CommentWriter,
  context: string,
  content: string,
  run: string,
  attempt: number,
): Promise<void> {
  const marker = `<!-- tsio-triage:${context} -->`;
  const comments = await writer.list();
  const existing = comments.find((c) => c.user?.type === "Bot" && c.body?.startsWith(marker));
  const previous = existing?.body?.match(/<!-- tsio-attempt:(\d+):(\d+) -->/);
  if (
    previous &&
    (BigInt(previous[1]) > BigInt(run) || (previous[1] === run && Number(previous[2]) > attempt))
  )
    return;
  const body = `${marker}\n<!-- tsio-attempt:${run}:${attempt} -->\n${content}`.slice(0, 65000);
  if (existing?.body === body) return;
  if (existing) await writer.update(existing.id, body);
  else await writer.create(body);
}
export function manualStatus(status: { state: string; description: string | null }): boolean {
  // The dispatch-only web override uses (verified). Mobile label waivers must
  // always be revalidated against live labels, not historical status text.
  return status.state === "success" && /\(verified\)/i.test(status.description || "");
}
export function overrideDescription(label: string, actor: string): string {
  if (label === "E2E/Verified") return `TSIO triage — verified flaky by ${actor}`.slice(0, 140);
  if (label === "E2E/Override") return "TSIO triage - e2e overridden";
  if (label === "manual-status-verified") return "TSIO triage (verified)";
  return `${label}: human override`.slice(0, 140);
}
