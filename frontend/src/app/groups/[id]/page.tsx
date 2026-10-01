"use client";

import { use, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { Github, LogOut, Users, UserPlus } from "lucide-react";
import { CollectiveDetailView } from "@peasant-labs/fairtrade/commons";
import { Toast } from "@/lib/ft-ui";
import {
  useGroup,
  useGroupTranscripts,
  useJoinGroup,
  useMyGroupShares,
  useRemoveGroupMember,
} from "@/lib/queries/groups";
import {
  isNotConfigured,
  useAvailableRepositories,
  useLinkRepository,
  useRepositories,
  useUnlinkRepository,
} from "@/lib/queries/repositories";
import { githubInstallURL } from "@/lib/githubInstall";
import { useAuth } from "@/providers/AuthProvider";
import {
  collectiveMembers,
  collectiveStats,
  collectiveTranscriptRow,
  isMemberRole,
  linkedRepoIds,
  memberBreakdown,
  orgSummary,
  pullRequestCells,
  repoPickerOwners,
  whoCanPublishText,
  whoCanReadText,
  yourRoleText,
  type PolicyInput,
} from "@/lib/adapters/collective";
import { applyRepoLinks, repoLinkMessage, repoLinkSteps, splitRepo } from "@/lib/repoLinks";
import LeaveCollectiveDialog from "@/components/group/LeaveCollectiveDialog";
import JoinConsentDialog from "@/components/group/JoinConsentDialog";

/** How many transcripts the table shows first, and how many more each `show more` adds. */
const PAGE_SIZE = 20;

/** One line the page tells the viewer after an action: saved, or what went wrong. */
interface Notice {
  ok: boolean;
  title: string;
  detail: string;
}

export default function GroupDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { user } = useAuth();
  const collective = useGroup(id);
  const { data, isLoading } = collective;

  const role = data?.your_role ?? "";
  const isOwner = role === "owner";
  const isMember = isMemberRole(role);
  const canRead = !!data?.can_read;
  const linkedOrg = data?.group.linked_github_org ?? null;

  // The first page arrives with the collective; `show more` asks for a longer
  // first page, so the rows already shown stay in place.
  const [shown, setShown] = useState(PAGE_SIZE);
  const longer = useGroupTranscripts(id, 0, shown, canRead && shown > PAGE_SIZE);

  // Linked repositories are readable by members; what the App offers is the owner's alone.
  const repositories = useRepositories(id, isMember);
  const available = useAvailableRepositories(id, isOwner && !!linkedOrg);
  const linkRepository = useLinkRepository();
  const unlinkRepository = useUnlinkRepository();

  const joinGroup = useJoinGroup();
  const removeMember = useRemoveGroupMember();
  const canLeave = !!role && role !== "owner";
  const { data: myShares } = useMyGroupShares(id, !!user && canLeave);
  const [confirmingLeave, setConfirmingLeave] = useState(false);
  const [showJoinConsent, setShowJoinConsent] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);

  if (isLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="h-4 w-40 bg-surface-hover animate-shimmer" />
        <div className="h-16 w-72 bg-surface-hover animate-shimmer" />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-24 bg-surface-hover animate-shimmer" />
          ))}
        </div>
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
      </div>
    );
  }

  if (collective.isError) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12">
        <div role="alert" className="text-[var(--fs-body)] text-danger">
          <p>could not read the collective: {collective.error.message}</p>
          <button type="button" className="btn btn-secondary btn-sm" disabled={collective.isFetching} onClick={() => void collective.refetch()}>try again</button>
        </div>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <Users size={28} className="text-ink-4" aria-hidden="true" />
          <p className="text-sm font-medium text-ink">collective not found</p>
          <Link
            href="/groups"
            className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            back to collectives
          </Link>
        </div>
      </div>
    );
  }

  const { group, stats } = data;
  const now = new Date();
  const members = collectiveMembers(data.members ?? []);
  const membersVisible = group.display_members || isOwner;

  const readable = shown > PAGE_SIZE && longer.data ? longer.data : (data.transcripts ?? []);
  const cells = pullRequestCells(readable);
  const rows = readable.map((t) => collectiveTranscriptRow(t, cells, { id: user?.id, isOwner }, now));
  const total = stats?.total_transcripts ?? rows.length;

  const policy: PolicyInput = {
    name: group.name,
    acceptanceMode: group.acceptance_mode,
    dataAccess: group.data_access,
    linkedOrg,
    role,
    canRead,
  };

  // A 501 on the linked list is the server saying GitHub is not set up here:
  // nothing can be linked, so nothing offers to.
  const githubConfigured = !(repositories.isError && isNotConfigured(repositories.error));
  const linked = repositories.data?.repositories ?? [];
  const offered = available.data?.repositories ?? [];
  const owners = repoPickerOwners(offered, linked, linkedOrg);
  const canManageRepos = isOwner && !!linkedOrg && githubConfigured && repositories.isSuccess;
  // No org yet, or an org the App is not installed on (it offers nothing and
  // nothing is linked): the owner's way forward is the install handshake.
  const needsInstall =
    isOwner &&
    githubConfigured &&
    repositories.isSuccess &&
    (!linkedOrg || (available.isSuccess && offered.length === 0 && linked.length === 0));

  const canJoin = !!user && role === "" && group.acceptance_mode !== "curated";

  const handleJoin = () => {
    if (user && !user.is_discoverable) {
      setShowJoinConsent(true);
      return;
    }
    joinGroup.mutate(id, {
      onError: (error) => setNotice({ ok: false, title: "could not join", detail: error.message }),
    });
  };

  const handleLinkRepos = async (diff: { add: string[]; remove: string[] }) => {
    const outcome = await applyRepoLinks(repoLinkSteps(diff), (step) => {
      const { owner, name } = splitRepo(step.repo);
      return step.action === "link"
        ? linkRepository.mutateAsync({ groupId: id, owner, name })
        : unlinkRepository.mutateAsync({ groupId: id, owner, name });
    });
    await qc.invalidateQueries({ queryKey: ["group-repositories", id] });
    await qc.invalidateQueries({ queryKey: ["group-repositories-available", id] });
    setNotice(repoLinkMessage(outcome));
  };

  const hrefFor = (kind: string, target?: string): string | undefined => {
    switch (kind) {
      case "collectives":
        return "/groups";
      case "collective":
        return target ? `/groups/${target}` : undefined;
      case "transcript":
        return target ? `/transcripts/${target}` : undefined;
      case "pull-request":
        return target ? cells.hrefs.get(target) : undefined;
      default:
        return undefined;
    }
  };

  const headerActions = [
    canJoin ? (
      <button
        key="join"
        type="button"
        className="btn btn-primary btn-sm"
        disabled={joinGroup.isPending}
        aria-busy={joinGroup.isPending || undefined}
        onClick={handleJoin}
      >
        <UserPlus size={14} aria-hidden="true" /> {joinGroup.isPending ? "joining" : "join"}
      </button>
    ) : null,
    needsInstall ? (
      <button
        key="connect"
        type="button"
        className="btn btn-secondary btn-sm"
        onClick={() => window.location.assign(githubInstallURL(id))}
      >
        <Github size={14} aria-hidden="true" /> connect github
      </button>
    ) : null,
    canLeave ? (
      <button key="leave" type="button" className="btn btn-ghost btn-sm" onClick={() => setConfirmingLeave(true)}>
        <LogOut size={14} aria-hidden="true" /> leave
      </button>
    ) : null,
  ].filter(Boolean);

  return (
    <div className="cmg-root max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-4 animate-fade-up">
      {/* The view names no join, leave or connect action, so the page draws
          them on the breadcrumb's line, above the view's own settings and more
          actions: static above it on a small screen, beside it from md up. The
          box is the view's own content column (fairtrade's .iu-page: 1184px
          wide, 24px above and 16px beside its content), so an action lines up
          with the breadcrumb it sits beside. */}
      <div className="relative mx-auto w-full max-w-[1184px]">
        {headerActions.length > 0 && (
          <div
            className="flex flex-wrap items-center justify-end gap-2 pt-3 md:absolute md:right-[var(--sp-4)] md:top-[calc(var(--sp-5)-4px)] md:z-[1] md:pt-0"
            data-testid="collective-header-actions"
          >
            {headerActions}
          </div>
        )}
        {notice && (
          <div className="px-[var(--sp-4)] pt-[var(--sp-5)]" data-testid="collective-notice">
            <Toast variant={notice.ok ? "ok" : "err"} title={notice.title} onClose={() => setNotice(null)}>
              {notice.detail}
            </Toast>
          </div>
        )}
        <CollectiveDetailView
          data={{
            collective: {
              id: group.id,
              name: group.name,
              purpose: group.description,
              role,
              whoCanRead: whoCanReadText(policy),
              whoCanPublish: whoCanPublishText(policy),
              yourRole: yourRoleText(policy),
              stats: collectiveStats(stats, members.length),
              transcriptCount: total,
              transcripts: rows,
              org: orgSummary(linkedOrg, repositories.data ? linked.length : undefined, available.data ? offered.length : undefined),
              memberBreakdown: membersVisible ? memberBreakdown(members) : "the owner keeps the member list private.",
              members: membersVisible ? members.map((m) => ({ handle: `@${m.github_username}`, name: m.display_name ?? undefined, role: m.role })) : [],
              memberCount: membersVisible ? members.length : undefined,
            },
            owners,
            linkedRepos: linkedRepoIds(linked, owners),
            hrefFor,
          }}
          actions={{
            onSettings: isOwner ? () => router.push(`/groups/${id}/settings`) : undefined,
            onContribute: isMember ? () => router.push(`/groups/${id}/contribute`) : undefined,
            onReview: isOwner && group.acceptance_mode === "curated" ? () => router.push(`/groups/${id}/review`) : undefined,
            onOpenTranscript: (transcriptId: string) => router.push(`/transcripts/${transcriptId}`),
            onOpenPullRequest: (label: string) => {
              const href = cells.hrefs.get(label);
              if (href) router.push(href);
            },
            onLinkRepos: canManageRepos ? (diff) => void handleLinkRepos(diff) : undefined,
            onShowMore: canRead && total > rows.length ? () => setShown((count) => count + PAGE_SIZE) : undefined,
          }}
        />
      </div>

      {user && canLeave && (
        <LeaveCollectiveDialog
          open={confirmingLeave}
          onClose={() => setConfirmingLeave(false)}
          onConfirm={(retract) =>
            removeMember.mutate(
              { groupId: id, userId: user.id, retract },
              {
                onSuccess: () => setConfirmingLeave(false),
                onError: (error) => {
                  setConfirmingLeave(false);
                  setNotice({ ok: false, title: "could not leave", detail: error.message });
                },
              },
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
          joinGroup.mutate(id, {
            onSuccess: () => setShowJoinConsent(false),
            onError: (error) => {
              setShowJoinConsent(false);
              setNotice({ ok: false, title: "could not join", detail: error.message });
            },
          })
        }
        collectiveName={group.name}
        isSubmitting={joinGroup.isPending}
      />
    </div>
  );
}
