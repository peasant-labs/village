/**
 * Test-only expansion of the compact tokens the contribution fixtures use, so
 * a fixture can state a family in one line and still decode against the
 * contract. Imported by tests only.
 */
export const GROUP_ID = "0c0c0c0c-0000-4000-8000-000000000001";
export const OTHER_GROUP_ID = "0c0c0c0c-0000-4000-8000-000000000002";

/** "t7" -> a transcript id ending in 7. */
export function transcriptIdOf(token: string): string {
  const n = Number(token.replace(/^\$?t/, ""));
  if (!Number.isInteger(n) || n < 0) throw new Error(`not a transcript token: ${token}`);
  return `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
}

/** "pA" -> a 64-hex project hash derived from the letter. */
export function projectHashOf(token: string): string {
  const name = token.replace(/^\$?p/, "");
  if (!/^[A-Z]$/.test(name)) throw new Error(`not a project token: ${token}`);
  return name.charCodeAt(0).toString(16).padStart(2, "0").repeat(32);
}

export interface TranscriptToken {
  id: string;
  project: string;
  already_shared?: boolean;
  visibility?: string;
  status?: string;
  title?: string | null;
  provider?: string;
}

/** A full contributable transcript from its compact token. */
export function transcriptOf(token: TranscriptToken): Record<string, unknown> {
  const id = transcriptIdOf(token.id);
  const n = token.id.replace(/^\$?t/, "");
  return {
    id,
    local_id: `sess_${n}`,
    title: token.title === undefined ? `session ${n}` : token.title,
    project_hash: projectHashOf(token.project),
    project_display_name: `project ${token.project.replace(/^\$?p/, "").toLowerCase()}`,
    project_name_source: "remote",
    git_branch: "main",
    parent_session_id: null,
    session_origin: "user",
    model_provider: token.provider ?? "anthropic",
    published_at: "2026-11-01T00:00:00Z",
    visibility: token.visibility ?? "private",
    already_shared: token.already_shared ?? false,
    ...(token.status ? { latest_status: token.status } : {}),
  };
}

/** Recursively expands "$group", "$other_group", "$t<n>", "$p<X>" and
 *  { $transcript: {...} } inside a fixture value. */
export function expandContributionTokens(value: unknown): unknown {
  if (typeof value === "string") {
    if (value === "$group") return GROUP_ID;
    if (value === "$other_group") return OTHER_GROUP_ID;
    if (/^\$t\d+$/.test(value)) return transcriptIdOf(value);
    if (/^\$p[A-Z]$/.test(value)) return projectHashOf(value);
    return value;
  }
  if (Array.isArray(value)) return value.map(expandContributionTokens);
  if (typeof value === "object" && value !== null) {
    const record = value as Record<string, unknown>;
    if ("$transcript" in record) return transcriptOf(record.$transcript as TranscriptToken);
    return Object.fromEntries(Object.entries(record).map(([key, inner]) => [key, expandContributionTokens(inner)]));
  }
  return value;
}
