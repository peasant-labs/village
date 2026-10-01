/**
 * The collective pages' view models: what the fairtrade collectives views
 * (`CollectivesView`, `CollectiveDetailView`, `CollectiveSettingsView`) and the
 * repo picker read, built from the wire. Every function here is pure; the pages
 * fetch and write, and pass the results through.
 *
 * The copy in the three policy boxes restates what the server already decided
 * (`data_access`, `acceptance_mode`, `your_role`, `can_read`). It decides
 * nothing: if the server's rule changes, this copy is what has to move with it.
 */
import type { ComponentProps } from "react";
import {
  zVillageUpdateGroupRequest,
  type VillageAvailableRepository,
  type VillageCollectiveSearchResult,
  type VillageUpdateGroupRequest,
} from "@peasant-labs/schema";
import type {
  CollectiveDetailView,
  CollectiveSettingsView,
  CollectivesView,
} from "@peasant-labs/fairtrade/commons";
import { formatCompact, resolveAttribution } from "@/lib/format";
import type {
  Group,
  GroupMember,
  GroupTranscript,
  GroupTranscriptStats,
  LinkedRepository,
  VisibleGroup,
} from "@/lib/types";

type ListData = NonNullable<NonNullable<ComponentProps<typeof CollectivesView>>["data"]>;
type DetailData = NonNullable<NonNullable<ComponentProps<typeof CollectiveDetailView>>["data"]>;
type SettingsData = NonNullable<NonNullable<ComponentProps<typeof CollectiveSettingsView>>["data"]>;

export type CollectiveRow = NonNullable<ListData["collectives"]>[number];
export type CollectiveData = NonNullable<DetailData["collective"]>;
export type PolicyChoice = NonNullable<SettingsData["whoCanPublish"]>[number];
export type CollectiveTranscriptRow = NonNullable<CollectiveData["transcripts"]>[number];

/** The caller's standing in a collective, as `GET /groups/{id}` states it. */
export type CollectiveRole = "" | "pending" | "contributor" | "member" | "owner";

/** A role that belongs to the collective, as opposed to a pending request. */
export function isMemberRole(role: string | null | undefined): boolean {
  return role === "owner" || role === "member" || role === "contributor";
}

// ── time ─────────────────────────────────────────────────────────────────────

/**
 * When something happened, relative to `now`: `12m ago`, `2h ago`, `yesterday`,
 * `3d ago`, `last week`, then the date. An unreadable timestamp says nothing.
 */
export function relativeWhen(iso: string | null | undefined, now: Date): string | undefined {
  if (!iso) return undefined;
  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return undefined;
  const minutes = Math.floor((now.getTime() - then.getTime()) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days === 1) return "yesterday";
  if (days < 7) return `${days}d ago`;
  if (days < 14) return "last week";
  return then.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: then.getFullYear() === now.getFullYear() ? undefined : "numeric",
  });
}

/** "member for 5mo" / "joined today", from the caller's join date. */
export function memberSince(iso: string | null | undefined, now: Date): string | null {
  if (!iso) return null;
  const joined = new Date(iso);
  if (Number.isNaN(joined.getTime())) return null;
  const days = Math.floor((now.getTime() - joined.getTime()) / 86_400_000);
  if (days < 1) return "joined today";
  if (days < 31) return `member for ${days}d`;
  const months = Math.floor(days / 30);
  if (months < 12) return `member for ${months}mo`;
  return `member for ${Math.floor(months / 12)}y`;
}

// ── the collectives list ─────────────────────────────────────────────────────

/** The caller's standing on one list row: their role, and whether they contributed. */
export interface RowStanding {
  memberRole: string | null;
  hasContributed: boolean;
}

/**
 * One row of the collectives table from a collective the caller may see. The
 * role cell states the caller's role when they belong, and adds `contributed`
 * when the collective holds or is reviewing something of theirs; a collective
 * the caller only sees states nothing there.
 */
export function collectiveListRow(group: VisibleGroup, standing: RowStanding, now: Date): CollectiveRow {
  return {
    id: group.id,
    name: group.name,
    desc: group.description,
    role: [standing.memberRole, standing.hasContributed ? "contributed" : null]
      .filter(Boolean)
      .join(" · "),
    since: memberSince(group.member_since, now),
    mode: group.acceptance_mode,
    members: group.member_count,
    transcripts: group.transcript_count,
    org: group.linked_github_org,
  };
}

/**
 * The table rows for a search. A result the caller can already see keeps the
 * row the list draws for it (role, joining); one the list does not hold states
 * only what the search answered, so nothing is claimed about how it is joined.
 */
export function searchResultRows(
  results: readonly VillageCollectiveSearchResult[],
  listed: readonly CollectiveRow[],
): CollectiveRow[] {
  const byId = new Map(listed.map((row) => [row.id, row]));
  return results.map(
    (result) =>
      byId.get(result.id) ?? {
        id: result.id,
        name: result.name,
        desc: result.description,
        role: "",
        members: result.member_count,
        transcripts: result.transcript_count,
        org: result.linked_github_org,
      },
  );
}

// ── the three policy boxes ───────────────────────────────────────────────────

export interface PolicyInput {
  name: string;
  acceptanceMode: string;
  dataAccess: string;
  linkedOrg: string | null;
  role: string;
  canRead: boolean;
}

/** Who can read the transcripts published to the collective (`data_access`). */
export function whoCanReadText({ name, dataAccess }: PolicyInput): string {
  switch (dataAccess) {
    case "public":
      return "anyone can read the transcripts published here.";
    case "contributors":
      return `members and contributors of ${name} can read every transcript published here.`;
    default:
      return `members of ${name} can read every transcript published here.`;
  }
}

/** Who can join and publish (`acceptance_mode`), as the join and share handlers decide it. */
export function whoCanPublishText({ acceptanceMode, linkedOrg }: PolicyInput): string {
  switch (acceptanceMode) {
    case "verified_only":
      return linkedOrg
        ? `only members of the ${linkedOrg} github org can join and publish.`
        : "only people with a verified github org can join and publish.";
    case "curated":
      return "only people the owner invites can join. the owner approves each transcript before it appears.";
    default:
      return "anyone can join and publish. transcripts are approved automatically.";
  }
}

/** The caller's role in words (`your_role`, and `can_read` for a contributor). */
export function yourRoleText({ role, canRead, acceptanceMode }: PolicyInput): string {
  switch (role) {
    case "owner":
      return "owner: you manage settings and members.";
    case "member":
      return "member: you read and publish here.";
    case "contributor":
      return canRead
        ? "contributor: you publish and read here."
        : "contributor: you publish here. only members read the transcripts.";
    case "pending":
      return "pending: the owner has not accepted your request yet.";
    default:
      return acceptanceMode === "curated"
        ? "not a member: the owner invites people to join."
        : "not a member: join to publish here.";
  }
}

// ── stats and members ────────────────────────────────────────────────────────

/** The members that belong to the collective, owner first; a pending request is not a member. */
export function collectiveMembers(members: readonly GroupMember[]): GroupMember[] {
  const order: Record<string, number> = { owner: 0, member: 1, contributor: 2 };
  return members
    .filter((member) => isMemberRole(member.role))
    .sort((a, b) => (order[a.role] ?? 3) - (order[b.role] ?? 3));
}

/** "1 owner · 7 members · 4 contributors", leaving out a role nobody holds. */
export function memberBreakdown(members: readonly GroupMember[]): string {
  const counts = { owner: 0, member: 0, contributor: 0 };
  for (const member of members) {
    if (member.role in counts) counts[member.role as keyof typeof counts] += 1;
  }
  const part = (count: number, one: string, many: string) =>
    count > 0 ? `${count} ${count === 1 ? one : many}` : null;
  return [
    part(counts.owner, "owner", "owners"),
    part(counts.member, "member", "members"),
    part(counts.contributor, "contributor", "contributors"),
  ]
    .filter(Boolean)
    .join(" · ");
}

/** The stats line: transcripts, members, tokens, and the pull requests linked. */
export function collectiveStats(
  stats: GroupTranscriptStats | undefined,
  memberCount: number,
): { value: number | string; label: string }[] {
  return [
    { value: stats?.total_transcripts ?? 0, label: "transcripts" },
    { value: memberCount, label: "members" },
    { value: formatCompact(Number(stats?.total_tokens ?? 0)), label: "tokens" },
    { value: stats?.pull_request_count ?? 0, label: "pull requests linked" },
  ];
}

// ── the transcripts table and its pull requests ─────────────────────────────

const repoKey = (owner: string, name: string) => `${owner}/${name}`;

/** Each row's pull request labels and repository line, and where each label leads. */
export interface PullRequestCells {
  byTranscript: Map<string, { labels: string[]; repo: string | null }>;
  hrefs: Map<string, string>;
}

/**
 * The pull request cells of the transcripts table. A row names its pull
 * requests `#42, #45` with the repository beneath when they share one
 * repository and no other row on the page uses the same number for a different
 * repository; otherwise each label carries its repository (`acme/web#31`), so
 * every label leads to exactly one pull request. When a transcript is attached
 * to more pull requests than the row names, the repository line says how many.
 */
export function pullRequestCells(transcripts: readonly GroupTranscript[]): PullRequestCells {
  const shortTargets = new Map<string, Set<string>>();
  for (const t of transcripts) {
    const repos = new Set(t.pull_requests.recent.map((pr) => repoKey(pr.owner, pr.name)));
    if (repos.size !== 1) continue;
    for (const pr of t.pull_requests.recent) {
      const label = `#${pr.number}`;
      const targets = shortTargets.get(label) ?? new Set<string>();
      targets.add(repoKey(pr.owner, pr.name));
      shortTargets.set(label, targets);
    }
  }

  const byTranscript = new Map<string, { labels: string[]; repo: string | null }>();
  const hrefs = new Map<string, string>();
  for (const t of transcripts) {
    const recent = t.pull_requests.recent;
    const repos = [...new Set(recent.map((pr) => repoKey(pr.owner, pr.name)))];
    const short =
      repos.length === 1 && recent.every((pr) => shortTargets.get(`#${pr.number}`)?.size === 1);
    const labels = recent.map((pr) => {
      const label = short ? `#${pr.number}` : `${repoKey(pr.owner, pr.name)}#${pr.number}`;
      hrefs.set(
        label,
        `/pulls/${encodeURIComponent(pr.owner)}/${encodeURIComponent(pr.name)}/${pr.number}`,
      );
      return label;
    });
    const more = t.pull_requests.count > recent.length ? `${t.pull_requests.count} pull requests` : null;
    const repo = [short ? repos[0] : null, more].filter(Boolean).join(" · ") || null;
    byTranscript.set(t.id, { labels, repo });
  }
  return { byTranscript, hrefs };
}

/** One row of the transcripts table. */
export function collectiveTranscriptRow(
  t: GroupTranscript,
  cells: PullRequestCells,
  viewer: { id: string | undefined; isOwner: boolean },
  now: Date,
): CollectiveTranscriptRow {
  const attribution = resolveAttribution(
    { id: t.owner_id, github_username: t.owner_username, is_discoverable: t.owner_is_discoverable },
    viewer.id,
    viewer.isOwner,
  );
  const prs = cells.byTranscript.get(t.id);
  return {
    id: t.id,
    title: t.title || "untitled",
    harness: t.model_provider,
    turns: t.turn_count ?? undefined,
    author: attribution.anonymous ? "anon" : `@${attribution.label}`,
    pullRequests: prs?.labels ?? [],
    repo: prs?.repo ?? null,
    when: relativeWhen(t.published_at, now),
  };
}

// ── the github org and the repo picker ───────────────────────────────────────

/** One owner in the repo picker (fairtrade `RepoPickerOwner`). */
export interface RepoPickerOwner {
  id: string;
  login: string;
  kind: "org" | "user";
  avatarUrl?: string;
  repos: { id: string; name: string; private?: boolean; note?: string }[];
}

function publisherNote(count: number): string | undefined {
  if (count <= 0) return undefined;
  return count === 1 ? "1 member publishes from it" : `${count} members publish from it`;
}

/**
 * The repo picker's owners: the repositories the GitHub App offers the
 * collective, grouped by owner with the linked org first, plus any linked
 * repository the App no longer offers, so it can still be unlinked. A repository
 * is identified by `owner/name`.
 */
export function repoPickerOwners(
  available: readonly VillageAvailableRepository[],
  linked: readonly LinkedRepository[],
  linkedOrg: string | null,
): RepoPickerOwner[] {
  const owners = new Map<string, RepoPickerOwner>();
  const add = (owner: string, repo: RepoPickerOwner["repos"][number]) => {
    const key = owner.toLowerCase();
    let entry = owners.get(key);
    if (!entry) {
      const isOrg = !!linkedOrg && key === linkedOrg.toLowerCase();
      entry = {
        id: key,
        login: owner,
        kind: isOrg ? "org" : "user",
        avatarUrl: isOrg ? undefined : `https://avatars.githubusercontent.com/${encodeURIComponent(owner)}`,
        repos: [],
      };
      owners.set(key, entry);
    }
    if (!entry.repos.some((existing) => existing.id.toLowerCase() === repo.id.toLowerCase())) {
      entry.repos.push(repo);
    }
  };
  for (const repo of available) {
    const id = repoKey(repo.owner, repo.name);
    add(repo.owner, { id, name: id, private: repo.is_private, note: publisherNote(repo.publisher_count) });
  }
  for (const repo of linked) {
    const id = repoKey(repo.owner, repo.name);
    add(repo.owner, { id, name: id, private: repo.is_private });
  }
  const orgKey = linkedOrg?.toLowerCase();
  return [...owners.values()].sort((a, b) => {
    if (a.id === orgKey) return -1;
    if (b.id === orgKey) return 1;
    return a.login.localeCompare(b.login);
  });
}

/** The linked repositories as the picker identifies them, matched to the picker's own ids. */
export function linkedRepoIds(linked: readonly LinkedRepository[], owners: readonly RepoPickerOwner[]): string[] {
  const ids = new Map<string, string>();
  for (const owner of owners) for (const repo of owner.repos) ids.set(repo.id.toLowerCase(), repo.id);
  return linked.map((repo) => {
    const id = repoKey(repo.owner, repo.name);
    return ids.get(id.toLowerCase()) ?? id;
  });
}

/**
 * The github org line. `null` says nothing is linked. The `of M` total is the
 * owner's alone: only the owner may read what the App offers, so a member sees
 * the org without it.
 */
export function orgSummary(
  linkedOrg: string | null,
  linkedCount: number | undefined,
  availableCount: number | undefined,
): CollectiveData["org"] {
  if (!linkedOrg) return null;
  return {
    login: linkedOrg,
    linked: linkedCount,
    total: availableCount && availableCount > 0 ? availableCount : undefined,
  };
}

// ── the settings page ────────────────────────────────────────────────────────

/** The fields a settings change writes, as `CollectiveSettingsView` names them. */
export type CollectiveSettingKey =
  | "name"
  | "purpose"
  | "mode"
  | "access"
  | "memberLeaves"
  | "showOnPullRequests";

/**
 * The PATCH body for one settings change: exactly the one field the change
 * writes, proven by the contract's parser before anything is sent. The server
 * keeps every field the body leaves out. An empty name is refused here, before
 * the request, because a collective needs a name.
 */
export function settingPatch(key: CollectiveSettingKey, value: unknown): VillageUpdateGroupRequest {
  switch (key) {
    case "name": {
      const name = String(value ?? "").trim();
      if (!name) throw new Error("a collective needs a name.");
      return zVillageUpdateGroupRequest.parse({ name });
    }
    case "purpose":
      return zVillageUpdateGroupRequest.parse({ description: String(value ?? "") });
    case "mode":
      return zVillageUpdateGroupRequest.parse({ acceptance_mode: value });
    case "access":
      return zVillageUpdateGroupRequest.parse({ data_access: value });
    case "memberLeaves":
      return zVillageUpdateGroupRequest.parse({ transcript_deletion_policy: value });
    case "showOnPullRequests":
      return zVillageUpdateGroupRequest.parse({ post_prompts_check: Boolean(value) });
    default:
      throw new Error(`no setting is named ${JSON.stringify(key)}.`);
  }
}

/** The join choices, with the linked org named when there is one. */
export function whoCanPublishChoices(linkedOrg: string | null): PolicyChoice[] {
  return [
    { value: "open", label: "open", help: "anyone can join and publish. transcripts are approved automatically." },
    {
      value: "verified_only",
      label: "verified only",
      help: linkedOrg
        ? `only members of the ${linkedOrg} github org can join and publish.`
        : "only people with a verified github org can join and publish.",
    },
    { value: "curated", label: "curated", help: "you approve each transcript before it appears." },
  ];
}

/**
 * The read choices. `public` is not offered: publishing is for collectives,
 * and the public commons is hidden. A collective that is already public still
 * sees its own value, so the select never shows a value it has no option for.
 */
export function whoCanReadChoices(current: string): PolicyChoice[] {
  const choices: PolicyChoice[] = [
    { value: "members_only", label: "members only" },
    { value: "contributors", label: "members and contributors" },
  ];
  if (current === "public") choices.push({ value: "public", label: "anyone (public)" });
  return choices;
}

/**
 * What happens to a leaving member's transcripts, on the wire's deletion-policy
 * values: the row asks "what happens to the transcripts they published here",
 * and each choice answers it in a few words, short enough for a phone.
 */
export const MEMBER_LEAVES_CHOICES: PolicyChoice[] = [
  { value: "user_choice", label: "the member decides" },
  { value: "mandatory", label: "removed when they leave" },
];

/** A member as the settings list shows it: `@handle`, their name, their role. */
export function settingsMember(member: GroupMember): { handle: string; name?: string; role: string } {
  return {
    handle: `@${member.github_username}`,
    name: member.display_name ?? undefined,
    role: member.role,
  };
}

/** The member a settings row names, found by the `@handle` the row shows. */
export function memberByHandle(members: readonly GroupMember[], handle: string): GroupMember | undefined {
  const login = handle.replace(/^@/, "").toLowerCase();
  return members.find((member) => member.github_username.toLowerCase() === login);
}

/** The group fields the settings view reads, from the group the server returned. */
export function settingsCollective(group: Group): Pick<CollectiveData, "id" | "name" | "purpose" | "mode" | "access" | "memberLeaves"> {
  return {
    id: group.id,
    name: group.name,
    purpose: group.description ?? "",
    mode: group.acceptance_mode,
    access: group.data_access,
    memberLeaves: group.transcript_deletion_policy,
  };
}
