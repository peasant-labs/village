import type { VillageContributableTranscript, VillageContributionPageItem } from "@peasant-labs/schema";
import { formatSessionDate, UNKNOWN_BRANCH_LABEL } from "./tree";
import { loadedRows, type LoadedRow } from "./frozenSelection";

/**
 * The loaded families of ONE project section, arranged the way the contribute
 * page has always drawn a project: by branch, then by session. A family is a
 * root session and the helpers that ride with it; the server already decided
 * which sessions form a family and in what order, so this only groups.
 */
export interface PagedFamily {
  root: LoadedRow;
  /** The root's helpers, in the order the server listed them. */
  helpers: LoadedRow[];
  /** The root, then its helpers: the rows the family checkbox acts on. */
  rows: LoadedRow[];
}

export interface PagedBranch {
  /** The root's `git_branch`, or "" when it has none. */
  key: string;
  label: string;
  families: PagedFamily[];
  /** Every loaded row on the branch: the rows the branch checkbox acts on. */
  rows: LoadedRow[];
}

/**
 * Groups loaded families by their ROOT's branch. A helper on another branch
 * stays with its root, as it stays in its root's project. Named branches sort
 * by name and the unknown branch comes last; families keep the server's order
 * (most recent activity first) inside a branch.
 */
export function sectionBranches(items: readonly VillageContributionPageItem[]): PagedBranch[] {
  const byBranch = new Map<string, PagedBranch>();
  for (const item of items) {
    const [root, ...helpers] = loadedRows([item]);
    const key = root.transcript.git_branch ?? "";
    let branch = byBranch.get(key);
    if (!branch) {
      branch = { key, label: key || UNKNOWN_BRANCH_LABEL, families: [], rows: [] };
      byBranch.set(key, branch);
    }
    const family = { root, helpers, rows: [root, ...helpers] };
    branch.families.push(family);
    branch.rows.push(...family.rows);
  }
  return [...byBranch.values()].sort((a, b) => {
    if (a.key === "" || b.key === "") return a.key === "" ? (b.key === "" ? 0 : 1) : -1;
    return a.key.localeCompare(b.key);
  });
}

/** The time a session was last active: the order the paged view lists by. */
export function lastActivity(transcript: VillageContributableTranscript): string {
  return transcript.session_end ?? transcript.session_start ?? transcript.published_at;
}

/**
 * The mono line under a session's title: its harness, when it was last
 * active, its stored visibility, and, for a helper filed under another
 * project, that project. The page lists by last activity, so the date it
 * shows is that one.
 */
export function pagedRowMeta(row: LoadedRow): string {
  const transcript = row.transcript;
  const parts = [
    transcript.model_provider || "unknown harness",
    formatSessionDate(lastActivity(transcript)),
    transcript.visibility,
  ];
  if (transcript.project_hash !== row.family_project_hash) parts.push(`in ${transcript.project_display_name}`);
  return parts.join(" · ");
}
