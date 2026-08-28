"use client";

import { use, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Users,
  Lock,
  ExternalLink,
  Check,
  Minus,
  Trash2,
  ShieldAlert,
} from "lucide-react";
import {
  useGroup,
  useGroupTranscripts,
  useAddGroupMember,
  useRemoveGroupMember,
  useJoinGroup,
  usePromoteMember,
  useRemoveGroupTranscript,
  useMyGroupShares,
} from "@/lib/queries/groups";
import { useUnshareTranscript } from "@/lib/queries/transcripts";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { useAuth } from "@/providers/AuthProvider";
import { resolveAttribution } from "@/lib/format";
import {
  Button,
  ProviderName,
  ProviderBars,
  RailShell,
  RailSection,
  ModerationQueue,
  RoleRoster,
} from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import ProviderBadge from "@/components/transcript/ProviderBadge";
import GitHubUserSearch from "@/components/GitHubUserSearch";
import LeaveCollectiveDialog from "@/components/group/LeaveCollectiveDialog";
import JoinConsentDialog from "@/components/group/JoinConsentDialog";
import CollectiveAnalytics from "@/components/group/CollectiveAnalytics";
import CollectiveRepos from "@/components/group/CollectiveRepos";
import LinkedRepositories from "@/components/group/LinkedRepositories";
import Link from "next/link";
import { Manage } from "@peasant-labs/fairtrade/commons";
import { humanizeEnum } from "@/lib/adapters/manage";

// ── Helpers ────────────────────────────────────────────────────────────────────

function formatDuration(ms: number): string {
  const mins = Math.floor(ms / 60000);
  if (mins < 60) return `${mins}min`;
  const hrs = Math.floor(mins / 60);
  const remainMins = mins % 60;
  return remainMins > 0 ? `${hrs}h ${remainMins}m` : `${hrs}h`;
}

function formatCompact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1).replace(/\.0$/, "")}K`;
  return n.toLocaleString();
}

// ── Page ───────────────────────────────────────────────────────────────────────

export default function GroupDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const { user } = useAuth();
  const { data, isLoading } = useGroup(id);
  const addMember = useAddGroupMember();
  const removeMember = useRemoveGroupMember();
  const joinGroup = useJoinGroup();
  const promoteMember = usePromoteMember();
  const removeGroupTranscript = useRemoveGroupTranscript();
  const unshareTranscript = useUnshareTranscript();

  const [inviteUsername, setInviteUsername] = useState("");
  const [dataPage, setDataPage] = useState(0);
  const [showDataBrowser, setShowDataBrowser] = useState(false);
  const [contributorFilter, setContributorFilter] = useState<string>("");
  const [browseView, setBrowseView] = useState<"list" | "repos">("list");
  const [rowSelected, setRowSelected] = useState<Set<string>>(new Set());
  const [confirmingBulkRemove, setConfirmingBulkRemove] = useState(false);
  const [confirmingLeave, setConfirmingLeave] = useState(false);
  const [showJoinConsent, setShowJoinConsent] = useState(false);
  const qc = useQueryClient();

  const handleJoinClick = () => {
    if (user && !user.is_discoverable) {
      setShowJoinConsent(true);
    } else {
      joinGroup.mutate(id);
    }
  };

  const DATA_PAGE_SIZE = 50;

  const { data: pagedData, isFetching: isFetchingPage } = useGroupTranscripts(
    id, dataPage, DATA_PAGE_SIZE, showDataBrowser
  );

  interface PendingShare {
    transcript_id: string;
    title: string | null;
    model_provider: string;
    owner_username: string;
    owner_is_discoverable: boolean;
    shared_at: string;
  }

  const { data: pendingShares } = useQuery({
    queryKey: ["group-pending", id],
    queryFn: () => api<PendingShare[]>(`/groups/${id}/pending`),
    enabled: !!data && data.group?.acceptance_mode === "curated" && data.your_role === "owner",
  });

  const reviewShare = useMutation({
    mutationFn: ({ transcriptId, status }: { transcriptId: string; status: string }) =>
      api(`/groups/${id}/shares/${transcriptId}`, {
        method: "PATCH",
        body: JSON.stringify({ status }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["group-pending", id] });
      qc.invalidateQueries({ queryKey: ["group", id] });
    },
  });

  const { data: myShares } = useMyGroupShares(id, !!user);

  if (isLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="h-4 w-40 bg-surface-hover animate-shimmer" />
        <div className="h-16 w-72 bg-surface-hover animate-shimmer" />
        <div className="h-12 w-full bg-surface-hover animate-shimmer" />
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-24 bg-surface-hover animate-shimmer" />
          ))}
        </div>
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
      </div>
    );
  }

  if (!data) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <Users size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">Collective not found</p>
          <Link
            href="/groups"
            className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            Back to collectives
          </Link>
        </div>
      </div>
    );
  }

  const {
    group,
    members,
    transcripts,
    stats,
    models,
    contributors,
    can_read: canRead,
    your_role: yourRole,
    pending_members: pendingMembersRaw,
  } = data;
  const isMember = yourRole === "contributor" || yourRole === "member" || yourRole === "owner";
  const isOwner = yourRole === "owner";
  const canLeave = !!yourRole && yourRole !== "owner";
  const pendingMembers = pendingMembersRaw ?? [];

  const manageData = {
    collective: {
      name: group.name,
      description: group.description,
      linkedGithubOrg: group.linked_github_org,
      // Humanized for display ("members_only" -> "members only") -- the GovTile
      // renders these as plain text, not a typed enum, so the wire value's
      // underscore is a wire detail and never belongs on screen.
      acceptanceMode: humanizeEnum(group.acceptance_mode),
      dataAccess: humanizeEnum(group.data_access),
      role: yourRole || "",
      memberSince: group.member_since,
    },
    providerShare: models.map((model) => ({
      id: model.model_provider,
      pct: stats?.total_transcripts ? Math.round((model.transcript_count / stats.total_transcripts) * 100) : 0,
    })),
    pendingReview: (pendingShares ?? []).map((item: { transcript_id: string; title: string | null; owner_username: string }) => ({
      id: item.transcript_id,
      title: item.title,
      by: `@${item.owner_username}`,
    })),
    // Deliberately empty: <Manage>'s own internal RoleRoster (fairtrade src/ui/commons/
    // Manage.jsx, rendered when `members.length > 0`) never received onRole/onRemove
    // callbacks, so it rendered a SECOND, non-functional-looking members roster further down
    // the page (in the collective-analytics region) alongside the real, fully-wired one this
    // page renders in membersSectionBody -- a genuine duplicate discovered while wiring up
    // this wave's role-dropdown work (its role <select> looked interactive but silently
    // no-op'd on change, snapping back since nothing persisted the edit). Members data is
    // used ONLY for that internal roster in Manage.jsx (verified -- no other field derives
    // from it), so passing [] here cleanly suppresses the dead duplicate without touching the
    // shared component or affecting the demo's own illustrative rendering.
    members: [],
    redactions: [],
    browseRows: (showDataBrowser ? (pagedData ?? []) : (transcripts || []).slice(0, 5)).map((transcript) => ({
      title: transcript.title ?? "untitled transcript",
      contributor: `@${transcript.owner_username}`,
      providerId: transcript.model_provider,
      provider: transcript.model_provider,
      turns: `${transcript.turn_count ?? 0}`,
      tokens: `${transcript.token_count ?? 0}`,
      date: transcript.session_start ? new Date(transcript.session_start).toLocaleDateString("en-US", { month: "short", day: "numeric" }) : "unknown",
    })),
    roleOptions: [],
    initialRole: yourRole || "",
    initialShowRedaction: false,
    initialBrowseGated: !canRead,
    stats: {
      transcripts: formatCompact(stats?.total_transcripts ?? 0),
      projects: `${formatCompact(models.length)} providers`,
      tokens: formatCompact(stats?.total_tokens ?? 0),
      turns: `${formatCompact(stats?.total_turns ?? 0)} turns`,
      contributors: `${formatCompact(stats?.contributor_count ?? 0)} members`,
      hours: `${formatDuration(stats?.total_duration_ms ?? 0)} total`,
    },
  };

  const totalTranscripts = stats?.total_transcripts ?? 0;
  const totalPages = Math.ceil(totalTranscripts / DATA_PAGE_SIZE);
  const rawBrowserTranscripts = showDataBrowser
    ? (pagedData ?? [])
    : (transcripts || []).slice(0, 5);
  const ANON_FILTER_KEY = "__anon__";
  const browserTranscripts = contributorFilter
    ? rawBrowserTranscripts.filter((t) => {
        const attribution = resolveAttribution(
          {
            id: t.owner_id,
            github_username: t.owner_username,
            is_discoverable: t.owner_is_discoverable,
          },
          user?.id,
          isOwner,
        );
        const key = attribution.anonymous ? ANON_FILTER_KEY : t.owner_username;
        return key === contributorFilter;
      })
    : rawBrowserTranscripts;

  // Distinct contributors in the visible page, for the filter dropdown.
  // Hidden users collapse into a single "anon" entry.
  const visibleContributors = Array.from(
    new Map(
      rawBrowserTranscripts.map((t) => {
        const attribution = resolveAttribution(
          {
            id: t.owner_id,
            github_username: t.owner_username,
            is_discoverable: t.owner_is_discoverable,
          },
          user?.id,
          isOwner,
        );
        const key = attribution.anonymous ? ANON_FILTER_KEY : t.owner_username;
        return [
          key,
          {
            key,
            label: attribution.label,
            avatar_url: attribution.anonymous ? null : t.owner_avatar_url,
          },
        ];
      })
    ).values()
  ).sort((a, b) => a.label.localeCompare(b.label));

  const handleInvite = (e: React.FormEvent) => {
    e.preventDefault();
    addMember.mutate(
      { groupId: id, username: inviteUsername },
      { onSuccess: () => setInviteUsername("") }
    );
  };

  function toggleRow(tid: string) {
    setRowSelected((prev) => {
      const next = new Set(prev);
      if (next.has(tid)) next.delete(tid);
      else next.add(tid);
      return next;
    });
  }

  function toggleAllRows(items: typeof browserTranscripts) {
    const ids = items.map((t) => t.id);
    setRowSelected((prev) => {
      const next = new Set(prev);
      const allSelected = ids.every((id) => next.has(id));
      if (allSelected) {
        ids.forEach((id) => next.delete(id));
      } else {
        ids.forEach((id) => next.add(id));
      }
      return next;
    });
  }

  async function handleBulkRemove() {
    if (rowSelected.size === 0) return;
    const ids = Array.from(rowSelected);
    await Promise.all(
      ids.map((tid) =>
        removeGroupTranscript.mutateAsync({ groupId: id, transcriptId: tid })
      )
    );
    setRowSelected(new Set());
    setConfirmingBulkRemove(false);
  }

  const roleOrder: Record<string, number> = { owner: 0, member: 1, contributor: 2 };

  // Map API members to RoleRoster's RosterMember shape, matching the settings page's own
  // adapter (src/app/groups/[id]/settings/page.tsx). Sorted owner-first, same ordering the
  // previous hand-rolled list used.
  const detailRosterMembers = [...members]
    .sort((a, b) => (roleOrder[a.role] ?? 3) - (roleOrder[b.role] ?? 3))
    .map((m) => ({
      id: m.id,
      // RoleRoster derives its fallback avatar from the first display-handle
      // character, so include "@" here without changing the stored login.
      handle: `@${m.github_username}`,
      // RoleRoster renders the display name beneath the handle when present.
      name: m.display_name ?? undefined,
      role: m.role as "owner" | "member" | "contributor" | "guest",
      owner: m.role === "owner",
      avatar: m.avatar_url ?? undefined,
    }));

  // ── Rail: Members section body ─────────────────────────────────────────────
  const membersSectionBody = (
    <>
      {group.linked_github_org && (
        <div className="flex items-center gap-2.5 px-5 py-2.5 border-b border-rule">
          <img
            src={`https://avatars.githubusercontent.com/${group.linked_github_org}`}
            alt={`${group.linked_github_org} avatar`}
            className="size-7 border border-rule object-cover shrink-0"
          />
          <span className="text-[13px] text-ink truncate">
            @{group.linked_github_org}
          </span>
          <span className="text-[10px] font-mono text-ink-3 shrink-0">org access</span>
        </div>
      )}
      {/* RoleRoster, matching the settings page's own governance surface: an owner/admin
          viewer gets an editable role dropdown per non-owner member (wired to the same
          promoteMember/removeMember mutations settings uses) and can remove members inline;
          a non-owner viewer (any signed-in member reaching this page -- unlike settings,
          this page is NOT owner-gated) sees every role as plain read-only text via
          canManage={isOwner}, no dropdown, no remove action. The true owner's own row stays
          locked either way (RoleRoster's own owner flag). overflow/maxWidth wrapper matches
          sections-react/70-governance.jsx's RoleRoster specimen (932e). */}
      <div style={{ overflow: "auto", maxWidth: "100%" }}>
        <RoleRoster
          title="members"
          members={detailRosterMembers}
          roles={["contributor", "member"]}
          canManage={isOwner}
          onRole={(m, role) =>
            promoteMember.mutate({ groupId: id, userId: m.id!, role: role as "member" | "contributor" })
          }
          onRemove={async (m) => {
            await removeMember.mutateAsync({ groupId: id, userId: m.id! });
          }}
        />
      </div>
      {isOwner && (
        <form
          onSubmit={handleInvite}
          className="px-5 py-4 border-t border-rule flex flex-col gap-2"
        >
          <label className="v2-eyebrow">Invite member</label>
          <div className="flex gap-2">
            <GitHubUserSearch
              value={inviteUsername}
              onChange={setInviteUsername}
              onSelect={setInviteUsername}
              className="flex-1"
            />
            <Button type="submit" size="md" disabled={addMember.isPending}>
              Invite
            </Button>
          </div>
          {addMember.isError && (
            <p className="text-[11px] text-danger">{addMember.error?.message ?? "Unable to invite member."}</p>
          )}
        </form>
      )}
    </>
  );

  // ── Rail: right-rail composition ───────────────────────────────────────────
  const pendingSharesList = pendingShares ?? [];
  const mySharesList = myShares ?? [];
  const currentUserId = user?.id;

  const railContent = (
    <>
      {/* Pending member requests — ModerationQueue for owner-only approval.
          overflow/maxWidth wrapper matches sections-react/70-governance.jsx's own specimen (932e). */}
      {isOwner && pendingMembers.length > 0 && (
        <div style={{ overflow: "auto", maxWidth: "100%" }}>
          <ModerationQueue
            title="pending requests"
            items={pendingMembers.map((p) => ({
              id: p.id,
              kind: "member" as const,
              who: `@${p.github_username}`,
            }))}
            onApprove={(item: { id: string }) => {
              promoteMember.mutate({ groupId: id, userId: item.id, role: "contributor" });
            }}
            onReject={(item: { id: string }) => {
              removeMember.mutate({ groupId: id, userId: item.id });
            }}
          />
        </div>
      )}

      {/* Members — gated by display_members or owner. */}
      {(group.display_members || isOwner) && (
        <RailSection
          title="members"
          icon={Users}
          meta={String(members.length)}
          collapsible={members.length > 8}
          defaultOpen={members.length <= 8}
        >
          {membersSectionBody}
        </RailSection>
      )}

      {/* Linked repositories — has its own internal chrome; render as-is. */}
      {isMember && (
        <LinkedRepositories
          groupId={id}
          isOwner={isOwner}
          transcripts={browserTranscripts}
        />
      )}

      {/* About */}
      <RailSection title="about">
        <div className="px-5 py-4">
          <div className="flex items-center justify-between">
            <span className="text-[13px] text-ink-3">Created</span>
            <span className="text-xs font-mono text-ink tabular-nums">
              {new Date(group.created_at).toLocaleDateString("en-US", {
                month: "short",
                day: "numeric",
                year: "numeric",
              })}
            </span>
          </div>
        </div>
      </RailSection>

      {/* Providers — ProviderBars replaces the manual bar chart. */}
      {models && models.length > 0 && (
        <RailSection title="providers">
          <div className="px-5 py-4">
            <ProviderBars
              data={models.map((m) => ({
                label: m.model_provider,
                value: m.transcript_count,
              }))}
              total={stats?.total_transcripts}
            />
          </div>
        </RailSection>
      )}
    </>
  );

  // ── Canvas: main content column ────────────────────────────────────────────
  const canvasContent = (
    <div className="flex flex-col gap-6">
      {/* Contributors */}
      {contributors && contributors.length > 0 && (
        <div className="border border-rule bg-surface">
          <div className="flex items-center justify-between px-5 py-3 border-b border-rule">
            <span className="text-sm font-medium text-ink">contributors</span>
            <span className="text-xs font-mono text-ink-3 tabular-nums">
              {contributors.length}
            </span>
          </div>
          <div className="px-5 py-4 flex flex-wrap gap-2">
            {contributors.map((c) => (
              <Link
                key={c.id}
                href={`/users/${c.github_username}`}
                className="group/contrib flex items-center gap-2 border border-rule bg-surface px-2.5 py-1.5 transition-colors hover:bg-surface-hover focus-mono cursor-pointer"
              >
                {c.avatar_url ? (
                  <img
                    src={c.avatar_url}
                    alt=""
                    className="size-6 border border-rule object-cover"
                  />
                ) : (
                  <span className="size-6 border border-rule bg-surface-hover flex items-center justify-center text-[10px] font-mono font-semibold text-ink-2">
                    {c.github_username[0].toUpperCase()}
                  </span>
                )}
                <span className="flex flex-col leading-tight min-w-0">
                  <span className="text-[13px] text-ink truncate">
                    {c.github_username}
                  </span>
                  <span className="text-[10px] font-mono text-ink-3 tabular-nums">
                    {c.transcript_count} transcript
                    {c.transcript_count !== 1 ? "s" : ""}
                  </span>
                </span>
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Collective analytics */}
      {canRead && browserTranscripts.length > 0 && (
        <CollectiveAnalytics transcripts={browserTranscripts} />
      )}

      {/* Data browser */}
      {canRead ? (
        <div className="border border-rule bg-surface">
          <div className="flex items-center justify-between px-5 py-3 border-b border-rule">
            <span className="text-sm font-medium text-ink">
              {showDataBrowser ? "browse data" : "recent transcripts"}
            </span>
            <div className="flex items-center gap-3">
              <div className="inline-flex border border-rule overflow-hidden">
                <button
                  type="button"
                  onClick={() => setBrowseView("list")}
                  aria-pressed={browseView === "list"}
                  className={`px-2.5 py-1 text-[11px] font-mono transition-colors cursor-pointer focus-mono ${
                    browseView === "list"
                      ? "bg-ink text-canvas"
                      : "text-ink-3 hover:text-ink hover:bg-surface-hover"
                  }`}
                >
                  list
                </button>
                <button
                  type="button"
                  onClick={() => setBrowseView("repos")}
                  aria-pressed={browseView === "repos"}
                  className={`px-2.5 py-1 text-[11px] font-mono border-l border-rule transition-colors cursor-pointer focus-mono ${
                    browseView === "repos"
                      ? "bg-ink text-canvas"
                      : "text-ink-3 hover:text-ink hover:bg-surface-hover"
                  }`}
                >
                  repos
                </button>
              </div>
              {totalTranscripts > 5 && (
                <button
                  onClick={() => {
                    setShowDataBrowser(!showDataBrowser);
                    setDataPage(0);
                  }}
                  className="text-xs font-mono text-ink-3 hover:text-ink transition-colors cursor-pointer focus-mono"
                >
                  {showDataBrowser
                    ? "Show less"
                    : `Browse all ${totalTranscripts.toLocaleString()}`}
                </button>
              )}
            </div>
          </div>

          {transcripts && transcripts.length === 0 ? (
            <div className="px-5 py-8 text-center">
              <p className="text-[13px] text-ink-3">No transcripts shared yet.</p>
            </div>
          ) : browseView === "repos" ? (
            <div className="p-5">
              <CollectiveRepos
                transcripts={browserTranscripts}
                viewerId={user?.id}
                viewerIsOwner={isOwner}
              />
            </div>
          ) : transcripts && transcripts.length > 0 ? (
            <>
              {(visibleContributors.length > 1 || isOwner) && (
                <div className="flex items-center justify-between gap-3 px-5 py-2.5 border-b border-rule">
                  <div className="flex items-center gap-2">
                    <select
                      id="contributor-filter"
                      aria-label="Filter by contributor"
                      value={contributorFilter}
                      onChange={(e) => {
                        setContributorFilter(e.target.value);
                        setRowSelected(new Set());
                      }}
                      className="bg-canvas border border-rule text-ink text-[12px] font-mono px-2 py-1 focus-mono cursor-pointer"
                    >
                      <option value="">all contributors</option>
                      {visibleContributors.map((c) => (
                        <option key={c.key} value={c.key}>
                          {c.label}
                        </option>
                      ))}
                    </select>
                  </div>
                  {isOwner && rowSelected.size > 0 && (
                    <div className="flex items-center gap-2">
                      <span className="text-[11px] font-mono text-ink-3 tabular-nums">
                        {rowSelected.size} selected
                      </span>
                      {confirmingBulkRemove ? (
                        <>
                          <span className="font-mono text-[11px] text-ink-3">
                            Remove from collective?
                          </span>
                          <button
                            type="button"
                            disabled={removeGroupTranscript.isPending}
                            onClick={handleBulkRemove}
                            className="inline-flex items-center gap-1 h-7 px-2 text-[11.5px] font-medium border border-danger/40 bg-danger-soft text-danger hover:bg-danger hover:text-danger-fg focus-mono transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                          >
                            {removeGroupTranscript.isPending ? "Removing…" : "Yes"}
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmingBulkRemove(false)}
                            className="inline-flex items-center gap-1 h-7 px-2 text-[11.5px] font-medium border border-rule bg-surface text-ink-2 hover:bg-surface-hover focus-mono transition-colors cursor-pointer"
                          >
                            Cancel
                          </button>
                        </>
                      ) : (
                        <button
                          type="button"
                          onClick={() => setConfirmingBulkRemove(true)}
                          className="inline-flex items-center gap-1.5 h-7 px-2 text-[11.5px] font-medium border border-rule bg-surface text-ink-2 hover:bg-danger-soft hover:text-danger focus-mono transition-colors cursor-pointer"
                        >
                          <Trash2 size={11} strokeWidth={1.75} />
                          Remove from collective
                        </button>
                      )}
                    </div>
                  )}
                </div>
              )}
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-rule">
                      {isOwner && (
                        <th className="v2-eyebrow px-5 py-2.5 w-8">
                          <span
                            className={`size-3.5 border inline-flex items-center justify-center cursor-pointer transition-colors ${
                              browserTranscripts.length > 0 &&
                              browserTranscripts.every((t) => rowSelected.has(t.id))
                                ? "bg-mark border-mark"
                                : browserTranscripts.some((t) => rowSelected.has(t.id))
                                ? "bg-surface-hover border-rule-strong"
                                : "border-rule-strong hover:border-ink"
                            }`}
                            onClick={() => toggleAllRows(browserTranscripts)}
                            role="checkbox"
                            aria-checked={
                              browserTranscripts.length > 0 &&
                              browserTranscripts.every((t) => rowSelected.has(t.id))
                            }
                            tabIndex={0}
                          >
                            {browserTranscripts.length > 0 &&
                              browserTranscripts.every((t) => rowSelected.has(t.id)) && (
                                <Check className="size-2.5 text-mark-fg" strokeWidth={3} />
                              )}
                            {browserTranscripts.some((t) => rowSelected.has(t.id)) &&
                              !browserTranscripts.every((t) => rowSelected.has(t.id)) && (
                                <Minus className="size-2.5 text-ink" strokeWidth={3} />
                              )}
                          </span>
                        </th>
                      )}
                      <th className="text-left v2-eyebrow px-5 py-2.5">Title</th>
                      <th className="text-left v2-eyebrow px-5 py-2.5">Contributor</th>
                      <th className="text-left v2-eyebrow px-5 py-2.5">Provider</th>
                      <th className="text-right v2-eyebrow px-5 py-2.5">Turns</th>
                      <th className="text-right v2-eyebrow px-5 py-2.5">Tokens</th>
                      <th className="text-right v2-eyebrow px-5 py-2.5">Date</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-rule">
                    {browserTranscripts.map((t) => {
                      const isSelected = rowSelected.has(t.id);
                      return (
                        <tr
                          key={t.id}
                          className={`transition-colors ${
                            isSelected ? "bg-surface-hover" : "hover:bg-surface-hover"
                          }`}
                        >
                          {isOwner && (
                            <td className="px-5 py-2.5">
                              <button
                                type="button"
                                onClick={() => toggleRow(t.id)}
                                aria-label={
                                  isSelected ? "Deselect transcript" : "Select transcript"
                                }
                                className="shrink-0 cursor-pointer focus-mono"
                              >
                                <span
                                  className={`size-3.5 border flex items-center justify-center transition-colors ${
                                    isSelected
                                      ? "bg-mark border-mark"
                                      : "border-rule-strong hover:border-ink"
                                  }`}
                                >
                                  {isSelected && (
                                    <Check
                                      className="size-2.5 text-mark-fg"
                                      strokeWidth={3}
                                    />
                                  )}
                                </span>
                              </button>
                            </td>
                          )}
                          <td className="px-5 py-2.5">
                            <Link
                              href={`/transcripts/${t.id}`}
                              className="text-ink hover:underline transition-colors truncate block max-w-xs focus-mono cursor-pointer"
                            >
                              {t.title || "Untitled"}
                            </Link>
                          </td>
                          <td className="px-5 py-2.5">
                            {t.owner_is_discoverable === false &&
                            t.owner_id !== user?.id &&
                            !isOwner ? (
                              <span className="inline-flex items-center gap-1.5">
                                <span className="size-4 border border-rule bg-surface-hover flex items-center justify-center text-[8px] font-mono font-semibold text-ink-3">
                                  ?
                                </span>
                                <span className="text-[12px] text-ink-3">anon</span>
                              </span>
                            ) : (
                              <Link
                                href={`/users/${t.owner_username}`}
                                className="inline-flex items-center gap-1.5 focus-mono cursor-pointer hover:underline"
                              >
                                {t.owner_avatar_url ? (
                                  <img
                                    src={t.owner_avatar_url}
                                    alt=""
                                    className="size-4 border border-rule object-cover"
                                  />
                                ) : (
                                  <span className="size-4 border border-rule bg-surface-hover flex items-center justify-center text-[8px] font-mono font-semibold text-ink-2">
                                    {t.owner_username[0].toUpperCase()}
                                  </span>
                                )}
                                <span className="text-[12px] text-ink-2">
                                  {t.owner_username}
                                </span>
                              </Link>
                            )}
                          </td>
                          <td className="px-5 py-2.5">
                            {isHarness(t.model_provider) ? (
                              <ProviderName harness={t.model_provider} />
                            ) : (
                              <span className="text-xs font-mono text-ink-3">
                                {t.model_provider}
                              </span>
                            )}
                          </td>
                          <td className="px-5 py-2.5 text-right font-mono text-xs text-ink-3 tabular-nums">
                            {t.turn_count != null ? t.turn_count : "—"}
                          </td>
                          <td className="px-5 py-2.5 text-right font-mono text-xs text-ink-3 tabular-nums">
                            {t.token_count != null
                              ? formatCompact(t.token_count)
                              : t.tokens_in != null || t.tokens_out != null
                              ? formatCompact((t.tokens_in ?? 0) + (t.tokens_out ?? 0))
                              : "—"}
                          </td>
                          <td className="px-5 py-2.5 text-right font-mono text-xs text-ink-3 tabular-nums whitespace-nowrap">
                            {new Date(t.published_at).toLocaleDateString("en-US", {
                              month: "short",
                              day: "numeric",
                            })}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {showDataBrowser && totalPages > 1 && (
                <div className="flex items-center justify-between px-5 py-3 border-t border-rule">
                  <span className="text-[11px] font-mono text-ink-3 tabular-nums">
                    {isFetchingPage && (
                      <span className="mr-2 text-ink-4">Loading…</span>
                    )}
                    {dataPage * DATA_PAGE_SIZE + 1}
                    {"–"}
                    {Math.min((dataPage + 1) * DATA_PAGE_SIZE, totalTranscripts)} of{" "}
                    {totalTranscripts.toLocaleString()}
                  </span>
                  <div className="flex items-center gap-1">
                    <button
                      onClick={() => setDataPage(Math.max(0, dataPage - 1))}
                      disabled={dataPage === 0}
                      className="border border-rule px-2.5 py-1 text-xs font-mono text-ink-2 hover:bg-surface-hover hover:text-ink disabled:opacity-50 disabled:pointer-events-none transition-colors cursor-pointer focus-mono"
                    >
                      Prev
                    </button>
                    <span className="text-[11px] font-mono text-ink-3 px-2 tabular-nums">
                      {dataPage + 1} / {totalPages}
                    </span>
                    <button
                      onClick={() => setDataPage(Math.min(totalPages - 1, dataPage + 1))}
                      disabled={dataPage >= totalPages - 1}
                      className="border border-rule px-2.5 py-1 text-xs font-mono text-ink-2 hover:bg-surface-hover hover:text-ink disabled:opacity-50 disabled:pointer-events-none transition-colors cursor-pointer focus-mono"
                    >
                      Next
                    </button>
                  </div>
                </div>
              )}
            </>
          ) : null}
        </div>
      ) : (
        <div className="border border-rule bg-surface px-5 py-10 flex flex-col items-center gap-3 text-center">
          <Lock size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">Data access restricted</p>
          <p className="text-[13px] text-ink-3 max-w-sm">
            {group.data_access === "members_only"
              ? "Only full members can browse this collective's data."
              : "Only contributors and members can browse this collective's data."}
          </p>
          {!isMember && user && group.acceptance_mode === "open" && (
            // size="sm" -- every other action button on this surface (<Manage>'s join/leave/
            // contribute/settings, RailShell's invite) is the fairtrade small (28px) button; this
            // one uses the same small size so adjacent actions remain consistent.
            <Button
              variant="primary"
              size="sm"
              className="mt-1"
              loading={joinGroup.isPending}
              disabled={joinGroup.isPending}
              onClick={handleJoinClick}
            >
              {joinGroup.isPending ? "Joining…" : "Join as Contributor"}
            </Button>
          )}
        </div>
      )}

      {/* Pending shares — ModerationQueue for curated collectives.
          overflow/maxWidth wrapper matches sections-react/70-governance.jsx's own specimen (932e). */}
      {isOwner &&
        group.acceptance_mode === "curated" &&
        pendingSharesList.length > 0 && (
          <div style={{ overflow: "auto", maxWidth: "100%" }}>
            <ModerationQueue
              title="pending review"
              items={pendingSharesList.map((ps) => ({
                id: ps.transcript_id,
                kind: "share" as const,
                who: ps.title || "Untitled",
                detail: (
                  <span className="inline-flex items-center gap-1.5">
                    by @
                    {ps.owner_is_discoverable === false && !isOwner
                      ? "anon"
                      : ps.owner_username}
                    {" · "}
                    <Link
                      href={`/transcripts/${ps.transcript_id}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-0.5 text-ink-4 hover:text-ink transition-colors focus-mono"
                      onClick={(e) => e.stopPropagation()}
                      title="Preview transcript"
                    >
                      preview <ExternalLink size={10} className="inline-block" />
                    </Link>
                  </span>
                ),
              }))}
              onApprove={(item: { id: string }) => {
                reviewShare.mutate({ transcriptId: item.id, status: "approved" });
              }}
              onReject={(item: { id: string }) => {
                reviewShare.mutate({ transcriptId: item.id, status: "rejected" });
              }}
            />
          </div>
        )}

      {/* Your contributions */}
      {isMember && user && mySharesList.length > 0 && (
        <div className="border border-rule bg-surface">
          <div className="flex items-center justify-between gap-2 px-5 py-3 border-b border-rule">
            <span className="text-sm font-medium text-ink">Your contributions</span>
            <span className="text-xs font-mono text-ink-3 tabular-nums">
              {mySharesList.length}
            </span>
          </div>
          <div className="divide-y divide-rule">
            {mySharesList.map((s) => (
              <div
                key={s.id}
                className="flex items-center gap-3 px-5 py-2.5 hover:bg-surface-hover transition-colors"
              >
                <ProviderBadge provider={s.model_provider} />
                <Link
                  href={`/transcripts/${s.id}`}
                  className="text-sm text-ink truncate min-w-0 flex-1 hover:underline focus-mono cursor-pointer"
                >
                  {s.title || "Untitled"}
                </Link>
                {s.status === "pending" && (
                  <span className="text-[10px] font-mono text-ink-3 uppercase tracking-wider shrink-0">
                    pending
                  </span>
                )}
                <span className="text-[11px] font-mono text-ink-3 tabular-nums shrink-0">
                  {new Date(s.shared_at).toLocaleDateString("en-US", {
                    month: "short",
                    day: "numeric",
                  })}
                </span>
                <button
                  type="button"
                  onClick={() =>
                    unshareTranscript.mutate({ transcriptId: s.id, groupId: id })
                  }
                  disabled={unshareTranscript.isPending}
                  title="Unshare from this collective"
                  className="inline-flex size-7 items-center justify-center border border-rule bg-surface text-ink-3 hover:bg-danger-soft hover:text-danger focus-mono transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed shrink-0"
                >
                  <Trash2 className="size-3.5" />
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );

  return (
    <div className="cmg-root max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">

      {/* Manage owns the single "village > collectives > name" breadcrumb.

          An OWNER can contribute their own transcripts, but the shared manage
          surface renders its contribute action only for a member or a
          contributor, and exposes no header action slot -- so an owner had no
          way to reach this collective's contribute route. Village renders the
          same action itself, ONLY for an owner, so a member never sees two
          contribute buttons. It is deliberately the same control the manage
          surface renders for a member: fairtrade's primary small button, the
          same glyph the shipped surface uses at the same size, the same
          lowercase label. It is
          placed in the header band beside the breadcrumb (static above it on a
          small viewport, where an overlay could collide with a wrapped
          breadcrumb). Rendering it INSIDE the manage header row is a fairtrade
          change (a header action slot), not a village one. */}
      <div className="relative">
        {isOwner && (
          <div className="flex items-center justify-end pb-4 sm:pb-0 sm:absolute sm:right-0 sm:top-0">
            <Button
              variant="primary"
              size="sm"
              icon={ShieldAlert}
              onClick={() => router.push(`/groups/${id}/contribute`)}
            >
              contribute
            </Button>
          </div>
        )}
        <Manage
          data={manageData}
          actions={{
            onJoin: user ? handleJoinClick : undefined,
            onLeave: canLeave ? () => setConfirmingLeave(true) : undefined,
            // Owners get their own contribute button above (rendered
            // unconditionally by village, not gated on Manage's internal
            // role check), so this callback is withheld for owners here at
            // the village boundary. That keeps the no-double-button
            // guarantee village's own responsibility instead of depending
            // on Manage never rendering a contribute action for an owner.
            onContribute: isMember && !isOwner ? () => router.push(`/groups/${id}/contribute`) : undefined,
            onSettings: isOwner ? () => router.push(`/groups/${id}/settings`) : undefined,
          }}
        />
      </div>

      {/* Main layout — RailShell: main canvas + sticky right rail. NOT a duplicate of <Manage>
          above -- <Manage> only covers the governance summary (hero/GovTile/StatGrid, now
          removed here since <Manage> renders it); this RailShell carries content <Manage> has no
          equivalent for: the real data browser (list/repos toggle, contributor filter, bulk
          remove, pagination), CollectiveAnalytics, CollectiveRepos, LinkedRepositories, pending
          MEMBER join requests (distinct from <Manage>'s pending transcript-review queue), the
          contributors list, and the About/created-date section. */}
      <RailShell
        sheetTitle="details"
        rail={railContent}
      >
        {canvasContent}
      </RailShell>

      {/* Dialogs */}
      {user && canLeave && (
        <LeaveCollectiveDialog
          open={confirmingLeave}
          onClose={() => setConfirmingLeave(false)}
          onConfirm={(retract) =>
            removeMember.mutate(
              { groupId: id, userId: currentUserId ?? "", retract },
              { onSuccess: () => setConfirmingLeave(false) }
            )
          }
          collectiveName={group.name}
          policy={group.transcript_deletion_policy ?? "user_choice"}
          shareCount={myShares?.length ?? 0}
          isSubmitting={removeMember.isPending}
        />
      )}

      <JoinConsentDialog
        open={showJoinConsent}
        onClose={() => setShowJoinConsent(false)}
        onConfirm={() =>
          joinGroup.mutate(id, { onSuccess: () => setShowJoinConsent(false) })
        }
        collectiveName={group.name}
        isSubmitting={joinGroup.isPending}
      />
    </div>
  );
}
