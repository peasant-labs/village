/**
 * Format a model identifier into a human-readable name.
 * e.g. "claude-opus-4-5-20251101" → "Claude Opus 4.5"
 */
export function formatModelName(
  modelName: string | null | undefined,
  provider: string | null | undefined
): string {
  if (!modelName) {
    if (provider) {
      return provider.charAt(0).toUpperCase() + provider.slice(1).toLowerCase();
    }
    return "";
  }

  let s = modelName;

  // Strip date suffix (8+ digits at end after a dash)
  s = s.replace(/-\d{8,}$/, "");

  // Split on dashes
  const parts = s.split("-");

  // Combine adjacent digit groups into version numbers (e.g. ["4", "5"] → "4.5")
  const result: string[] = [];
  let i = 0;
  while (i < parts.length) {
    if (
      i + 1 < parts.length &&
      /^\d+$/.test(parts[i]) &&
      /^\d+$/.test(parts[i + 1])
    ) {
      result.push(`${parts[i]}.${parts[i + 1]}`);
      i += 2;
    } else {
      result.push(parts[i]);
      i++;
    }
  }

  // Title-case each word
  return result
    .map((w) => (w ? w.charAt(0).toUpperCase() + w.slice(1) : w))
    .join(" ");
}

/**
 * Extract a clean project display name from raw project metadata.
 * Prefers git remote URL, falls back to stripping known path prefixes
 * from the project name key.
 *
 * e.g. git remote "github.com/user/Neurondle.git" → "Neurondle"
 * e.g. "-Users-pigeonzow-Documents-GitHub-Neurondle" → "Neurondle"
 * e.g. "-Users-pigeonzow-Documents-GitHub-data-leverage-village" → "data-leverage-village"
 * e.g. "/Users/pigeonzow/Documents/GitHub/Neurondle" → "Neurondle"
 */
export function extractProjectDisplayName(
  projectName: string | null | undefined,
  gitRemote: string | null | undefined
): string | null {
  // Try git remote first
  if (gitRemote) {
    const remote = gitRemote.replace(/\.git$/, "");
    const idx = remote.lastIndexOf("/");
    if (idx >= 0) {
      const name = remote.substring(idx + 1);
      if (name) return name;
    }
  }

  if (!projectName) return null;

  // Slash-separated path: take last segment
  if (projectName.includes("/")) {
    const segments = projectName.split("/").filter(Boolean);
    return segments[segments.length - 1] || projectName;
  }

  // Dash-delimited path key (e.g. "-Users-pigeonzow-Documents-GitHub-project-name")
  // Strip leading dash, split into segments, find the last known directory
  // marker (like "GitHub", "Documents", "Projects", etc.) and take everything after it
  const knownDirs = ["GitHub", "Documents", "Projects", "repos", "src", "code", "dev", "home"];
  const stripped = projectName.replace(/^-/, "");
  const segments = stripped.split("-");

  let lastKnownIdx = -1;
  for (let i = 0; i < segments.length; i++) {
    if (knownDirs.some((d) => d.toLowerCase() === segments[i].toLowerCase())) {
      lastKnownIdx = i;
    }
  }

  if (lastKnownIdx >= 0 && lastKnownIdx < segments.length - 1) {
    return segments.slice(lastKnownIdx + 1).join("-");
  }

  return projectName;
}

/**
 * Group transcript list items by project name.
 * Returns groups sorted by most recent transcript, with transcripts
 * within each group sorted by published_at descending.
 */
export function groupByProject<
  T extends { transcript: { project_name: string | null; git_remote: string | null; published_at: string } }
>(items: T[]): { project: string; items: T[] }[] {
  const groups = new Map<string, T[]>();

  for (const item of items) {
    const key =
      extractProjectDisplayName(item.transcript.project_name, item.transcript.git_remote) ??
      "Other";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(item);
  }

  // Sort transcripts within each group by published_at desc
  for (const groupItems of groups.values()) {
    groupItems.sort(
      (a, b) =>
        new Date(b.transcript.published_at).getTime() -
        new Date(a.transcript.published_at).getTime()
    );
  }

  // Sort groups by most recent transcript
  return Array.from(groups.entries())
    .map(([project, groupItems]) => ({ project, items: groupItems }))
    .sort(
      (a, b) =>
        new Date(b.items[0].transcript.published_at).getTime() -
        new Date(a.items[0].transcript.published_at).getTime()
    );
}

/**
 * A group of a collective's transcripts that all belong to the same
 * repository, keyed off each transcript's git remote (with a project-name
 * fallback). Carries pre-aggregated rollups so the UI can render the repo
 * card without re-walking the transcript list.
 */
export interface RepoGroup<T> {
  /** Stable grouping key — the raw git remote when present, else a synthetic key. */
  key: string;
  /** Human-readable repo name derived from the remote/project. */
  name: string;
  /** Raw git remote, or null when the transcripts had no attributable remote. */
  remote: string | null;
  /** True when this is the catch-all bucket for transcripts with no git context. */
  unattributed: boolean;
  transcripts: T[];
  transcriptCount: number;
  /** Distinct contributor count (by owner id). */
  contributorCount: number;
  totalTokens: number;
}

const UNATTRIBUTED_KEY = "__unattributed__";

/**
 * Total tokens for a transcript, preferring the stored `token_count` and
 * falling back to the in/out split — matching the collective data browser.
 */
function transcriptTokens(t: {
  token_count: number | null;
  tokens_in: number | null;
  tokens_out: number | null;
}): number {
  if (t.token_count != null) return t.token_count;
  return (t.tokens_in ?? 0) + (t.tokens_out ?? 0);
}

/**
 * Group a collective's transcripts by repository.
 *
 * The repo key is derived from each transcript's `git_remote` (the same field
 * the local peasant app uses to relate transcripts to repos/commits). When a
 * transcript carries no remote, it falls into a single "Unattributed" bucket.
 * Groups are sorted by transcript count descending (Unattributed always last),
 * and transcripts within a group are sorted by `published_at` descending.
 */
export function groupByRepo<
  T extends {
    owner_id: string;
    git_remote: string | null;
    project_name: string | null;
    published_at: string;
    token_count: number | null;
    tokens_in: number | null;
    tokens_out: number | null;
  }
>(items: T[]): RepoGroup<T>[] {
  const buckets = new Map<string, T[]>();

  for (const item of items) {
    const remote = item.git_remote?.trim() || null;
    // Key on the raw remote so two transcripts with the same remote but
    // different project-name fallbacks still land together. Transcripts with
    // no remote collapse into the shared Unattributed bucket.
    const key = remote ?? UNATTRIBUTED_KEY;
    if (!buckets.has(key)) buckets.set(key, []);
    buckets.get(key)!.push(item);
  }

  const groups: RepoGroup<T>[] = Array.from(buckets.entries()).map(
    ([key, groupItems]) => {
      const unattributed = key === UNATTRIBUTED_KEY;
      const remote = unattributed ? null : key;

      groupItems.sort(
        (a, b) =>
          new Date(b.published_at).getTime() -
          new Date(a.published_at).getTime()
      );

      const contributors = new Set(groupItems.map((i) => i.owner_id));
      const totalTokens = groupItems.reduce(
        (sum, i) => sum + transcriptTokens(i),
        0
      );

      const name = unattributed
        ? "Unattributed"
        : extractProjectDisplayName(
            groupItems[0].project_name,
            remote
          ) ?? "Unattributed";

      return {
        key,
        name,
        remote,
        unattributed,
        transcripts: groupItems,
        transcriptCount: groupItems.length,
        contributorCount: contributors.size,
        totalTokens,
      };
    }
  );

  // Sort by transcript count desc; the Unattributed bucket always sinks last
  // so attributed repos lead the list.
  return groups.sort((a, b) => {
    if (a.unattributed !== b.unattributed) return a.unattributed ? 1 : -1;
    return b.transcriptCount - a.transcriptCount;
  });
}

/**
 * Resolves how a transcript author should be attributed in lists.
 * Returns `{ anonymous: true }` when the author has opted out of being
 * discoverable AND the viewer is not the author themselves; in that case
 * UI should render "anon" with no link or avatar lookup. Otherwise the
 * caller is responsible for rendering the handle/avatar as usual.
 */
export function resolveAttribution(
  owner: { id: string; github_username: string; is_discoverable?: boolean },
  viewerId: string | undefined,
  viewerIsPrivileged = false,
): { anonymous: boolean; label: string } {
  const anonymous =
    owner.is_discoverable === false && owner.id !== viewerId && !viewerIsPrivileged;
  return {
    anonymous,
    label: anonymous ? "anon" : owner.github_username,
  };
}

/**
 * Returns a human-readable tooltip string for a visibility level.
 */
export function visibilityTooltip(
  visibility: string,
  shares?: { group_name: string }[]
): string {
  switch (visibility) {
    case "public":
      return "Visible to everyone";
    case "shared": {
      if (shares && shares.length > 0) {
        const names = shares.map((s) => s.group_name).join(", ");
        return `Shared with: ${names}`;
      }
      return "Shared with specific collectives";
    }
    case "private":
      return "Only visible to you";
    default:
      return visibility;
  }
}
