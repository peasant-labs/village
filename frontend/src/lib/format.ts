import { assertNameSourceExhaustive, type NameSource } from "@/lib/types";

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
 * Human-readable explanation of a {@link NameSource} tier, for a tooltip or
 * `title` attribute next to a rendered project name — so a viewer can tell
 * an owner-chosen name from one the resolver inferred. This is the one live
 * consumer that must handle every {@link NameSource} member: the `default`
 * branch calls {@link assertNameSourceExhaustive}, so adding a tier to the
 * union without adding a `case` here fails the BUILD (see
 * `NameSource`'s doc comment and the compile-time mutation this proves in
 * the slice's completion report).
 */
export function describeNameSource(source: NameSource): string {
  switch (source) {
    case "override":
      return "Renamed by the project owner";
    case "consented":
      return "From the transcript's stored project name";
    case "remote":
      return "From the transcript's git remote";
    case "privacy":
      return "Auto-generated from the project's privacy-safe identity";
    default:
      return assertNameSourceExhaustive(source);
  }
}

/**
 * Group transcript list items by project identity.
 *
 * Keyed on `project_hash` — a project has no row of its own; it IS the
 * distinct `(owner, project_hash)` pair. Two transcripts sharing a hash
 * always collapse into ONE
 * group and render the ONE server-resolved `project_display_name`, even
 * when their raw `project_name` columns disagree (one consented, one a
 * Peasant privacy label) — that mixed-name-same-hash case is exactly what
 * the old name-keyed grouping got wrong. There is no "Other" fallback:
 * `project_hash` is a required identity column enforced at the publish
 * boundary (migration `035_project_hash_required`), so a served transcript
 * missing one is a contract violation, not a normal case to paper over.
 *
 * Returns groups sorted by most recent transcript, with transcripts within
 * each group sorted by `published_at` descending.
 */
export function groupByProject<
  T extends {
    transcript: {
      project_hash: string | null;
      project_display_name: string;
      published_at: string;
    };
  }
>(items: T[]): { project: string; project_hash: string; items: T[] }[] {
  const groups = new Map<string, { displayName: string; items: T[] }>();

  for (const item of items) {
    const hash = item.transcript.project_hash;
    if (!hash) {
      // What: a transcript with no project_hash reached the grouping layer.
      // Why: project_hash is a required identity column enforced at the
      // publish boundary (migration 035_project_hash_required) — every
      // served transcript must carry one.
      // Where: groupByProject in src/lib/format.ts.
      // When: while grouping a fetched transcript list for the profile view.
      // What it means: either a pre-migration row reached this client, or
      // the backend contract was violated.
      // Fix: verify the backend migration and publish-boundary guard are
      // active; this UI cannot safely invent a grouping key.
      throw new Error(
        "groupByProject: transcript has no project_hash, but project_hash is a required identity " +
          "column (migration 035_project_hash_required) enforced at the publish boundary. Every " +
          "served transcript must carry one; verify the backend migration and publish guard are " +
          "active rather than grouping this transcript into a synthetic bucket."
      );
    }
    if (!groups.has(hash)) {
      groups.set(hash, { displayName: item.transcript.project_display_name, items: [] });
    }
    groups.get(hash)!.items.push(item);
  }

  // Sort transcripts within each group by published_at desc
  for (const group of groups.values()) {
    group.items.sort(
      (a, b) =>
        new Date(b.transcript.published_at).getTime() -
        new Date(a.transcript.published_at).getTime()
    );
  }

  // Sort groups by most recent transcript
  return Array.from(groups.entries())
    .map(([project_hash, group]) => ({
      project: group.displayName,
      project_hash,
      items: group.items,
    }))
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
    project_display_name: string;
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

      // Repo grouping is a DIFFERENT axis from project identity: this
      // bucket keys on the raw git_remote, and its "Unattributed" fallback
      // is NOT the deleted project-level "unattributed" concept — it fires
      // only when a transcript carries no remote at all, independent of
      // project_hash. The label for an ATTRIBUTED bucket, though, is still
      // "the human name of this repo's project" — exactly what
      // project_display_name already is (the one server-resolved name
      // every other surface renders), so it replaces the old
      // extractProjectDisplayName(project_name, remote) derivation here
      // too, rather than leaving a second, now-orphaned name algorithm
      // alive on this axis.
      const name = unattributed ? "Unattributed" : groupItems[0].project_display_name;

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
