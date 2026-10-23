"use client";

import { use, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { ClipboardCheck, FileStack, Github, Lock, LogIn, LogOut, MoreHorizontal, Settings, UserPlus } from "lucide-react";
import { Menu, RepoPicker, Toast } from "@/lib/ft-ui";
import { OnCrumb, OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import { useGroup, useGroupTranscripts, useJoinGroup, useMyGroupShares, useRemoveGroupMember } from "@/lib/queries/groups";
import { usePendingShares } from "@/lib/queries/groupReview";
import { githubInstallURL } from "@/lib/githubInstall";
import { isApiErrorStatus } from "@/lib/api";
import { loginHref } from "@/lib/signIn";
import { useAuth } from "@/providers/AuthProvider";
import {
  collectiveMembers,
  collectiveStats,
  collectiveTranscriptRow,
  isMemberRole,
  pullRequestCells,
  type PolicyInput,
} from "@/lib/adapters/collective";
import {
  TAB_PARAM,
  collectiveTabFrom,
  collectiveTabsFor,
  settingsSectionHref,
  type CollectiveTabId,
} from "@/lib/collectiveSections";
import LeaveCollectiveDialog from "@/components/group/LeaveCollectiveDialog";
import CollectiveTranscriptLibrary from "@/components/group/CollectiveTranscriptLibrary";
import JoinConsentDialog from "@/components/group/JoinConsentDialog";
import CollectiveTabs, { type CollectiveTab } from "@/components/collective/page/CollectiveTabs";
import CollectiveTranscriptsTable from "@/components/collective/page/CollectiveTranscriptsTable";
import CollectiveWaiting from "@/components/collective/page/CollectiveWaiting";
import CollectiveMembers from "@/components/collective/page/CollectiveMembers";
import CollectiveStats from "@/components/collective/page/CollectiveStats";
import PolicyChips from "@/components/collective/page/PolicyChips";
import { CollectiveNotFound, CollectiveReadError, CollectiveSkeleton } from "@/components/collective/page/CollectiveStates";
import { useCollectiveGithub } from "@/components/collective/page/useCollectiveGithub";
import "@/app/online-collective.css";

/** How many transcripts the table shows first, and how many more each `show more` adds. */
const PAGE_SIZE = 20;

/** One line the page tells the viewer after an action: saved, or what went wrong. */
interface Notice {
  ok: boolean;
  title: string;
  detail: string;
}

const CRUMB_ROOT = { label: "collectives", href: "/collectives" };

export default function GroupDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const { user, isLoading: authLoading } = useAuth();
  const collective = useGroup(id);
  const { data } = collective;

  const role = data?.your_role ?? "";
  const isOwner = role === "owner";
  const isMember = isMemberRole(role);
  const canRead = !!data?.can_read;
  const linkedOrg = data?.group.linked_github_org ?? null;
  const acceptanceMode = data?.group.acceptance_mode ?? "open";
  const reviews = isOwner && acceptanceMode === "curated";

  // The first page arrives with the collective; `show more` asks for a longer
  // first page, so the rows already shown stay in place.
  const [shown, setShown] = useState(PAGE_SIZE);
  const longer = useGroupTranscripts(id, 0, shown, canRead && shown > PAGE_SIZE);

  // Linked repositories are readable by members; what the App offers is the owner's alone.
  const github = useCollectiveGithub(id, { isOwner, canReadLinks: isMember, linkedOrg });
  const [pickerOpen, setPickerOpen] = useState(false);

  // The owner of a curated collective reads the queue the review page decides.
  const pending = usePendingShares(id, reviews);

  const joinGroup = useJoinGroup();
  const removeMember = useRemoveGroupMember();
  const canLeave = !!role && role !== "owner";
  const { data: myShares } = useMyGroupShares(id, !!user && canLeave);
  const [confirmingLeave, setConfirmingLeave] = useState(false);
  const [showJoinConsent, setShowJoinConsent] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);

  // The open tab lives in the address, so a link can open one and a reload keeps it.
  const offeredTabs = collectiveTabsFor({ role, acceptanceMode });
  const askedTab = searchParams.get(TAB_PARAM);
  const [chosenTab, setChosenTab] = useState<string | null>(askedTab);
  // A link that changes the tab while the page is open (the sidebar, say) wins.
  const [lastAsked, setLastAsked] = useState(askedTab);
  if (lastAsked !== askedTab) {
    setLastAsked(askedTab);
    setChosenTab(askedTab);
  }
  const tab = collectiveTabFrom(chosenTab, offeredTabs);
  const selectTab = (next: CollectiveTabId) => {
    setChosenTab(next);
    const query = new URLSearchParams(window.location.search);
    if (next === offeredTabs[0]?.id) query.delete(TAB_PARAM);
    else query.set(TAB_PARAM, next);
    const search = query.toString();
    window.history.replaceState(null, "", `${pathname}${search ? `?${search}` : ""}`);
  };

  // Pending covers the wait for the session too: the read is not sent until the
  // viewer is known, and "not found" must never show before it has answered.
  if (collective.isPending) return <CollectiveSkeleton crumb={[CRUMB_ROOT]} />;

  // A 404 is the friendly not-found state below; other failures say what broke.
  if (collective.isError && !isApiErrorStatus(collective.error, 404)) {
    return (
      <CollectiveReadError
        crumb={[CRUMB_ROOT]}
        message={collective.error.message}
        retrying={collective.isFetching}
        onRetry={() => void collective.refetch()}
      />
    );
  }

  if (!data) return <CollectiveNotFound />;

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

  const joinable = group.acceptance_mode !== "curated";
  const canJoin = !!user && role === "" && joinable;
  // Somebody reading without an account is asked to sign in where joining
  // would need one, and comes back here afterwards.
  const signInHere = loginHref(pathname);
  const offerSignIn = !authLoading && !user;

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
    setNotice(await github.saveLinks(diff));
  };

  const moreItems = [
    ...(isMember
      ? [{ label: "publish several transcripts", icon: FileStack, onSelect: () => router.push(`/collectives/${id}/contribute`) }]
      : []),
    ...(reviews
      ? [{ label: "review transcripts", icon: ClipboardCheck, onSelect: () => router.push(`/collectives/${id}/review`) }]
      : []),
  ];

  const actions = [
    moreItems.length > 0 ? <Menu key="more" icon={MoreHorizontal} ariaLabel="more" align="end" items={moreItems} /> : null,
    github.needsInstall ? (
      <button
        key="connect"
        type="button"
        className="btn btn-secondary"
        onClick={() => window.location.assign(githubInstallURL(id))}
      >
        <Github size={16} aria-hidden="true" /> connect github
      </button>
    ) : null,
    canLeave ? (
      <button key="leave" type="button" className="btn btn-ghost" onClick={() => setConfirmingLeave(true)}>
        <LogOut size={16} aria-hidden="true" /> leave
      </button>
    ) : null,
    isOwner ? (
      <Link key="settings" href={`/collectives/${id}/settings`} className="btn btn-secondary">
        <Settings size={16} aria-hidden="true" /> settings
      </Link>
    ) : null,
    isOwner ? (
      <Link key="invite" href={settingsSectionHref(id, "members")} className="btn btn-primary">
        <UserPlus size={16} aria-hidden="true" /> invite people
      </Link>
    ) : null,
    canJoin ? (
      <button
        key="join"
        type="button"
        className="btn btn-primary"
        disabled={joinGroup.isPending}
        aria-busy={joinGroup.isPending || undefined}
        onClick={handleJoin}
      >
        <UserPlus size={16} aria-hidden="true" /> {joinGroup.isPending ? "joining" : "join"}
      </button>
    ) : null,
    offerSignIn && joinable ? (
      <Link key="sign-in" href={signInHere} className="btn btn-primary">
        <LogIn size={16} aria-hidden="true" /> sign in to join
      </Link>
    ) : null,
  ].filter(Boolean);

  const restricted = (
    <OnEmpty
      icon={<Lock />}
      title="data access restricted"
      body={
        group.data_access === "members_only"
          ? "only members can read this collective's transcripts."
          : "only members and contributors can read this collective's transcripts."
      }
      headingLevel={3}
      testId="collective-restricted"
      actions={
        // Where the head already offers a sign-in to join, one way in is enough.
        offerSignIn && !joinable ? (
          <Link href={signInHere} className="btn btn-secondary">
            <LogIn size={16} aria-hidden="true" /> sign in
          </Link>
        ) : undefined
      }
    />
  );

  const transcriptsPanel = (
    <div className="on-section on-cpanel">
      {!canRead ? (
        restricted
      ) : rows.length === 0 ? (
        <OnEmpty
          icon={<FileStack />}
          title="no transcripts yet"
          body="sessions published to this collective show up here."
          headingLevel={3}
          testId="collective-no-transcripts"
        />
      ) : (
        <CollectiveTranscriptsTable
          rows={rows}
          pullRequestHref={(label) => cells.hrefs.get(label)}
          showStatus={group.acceptance_mode === "curated"}
          total={total}
          onShowMore={canRead && total > rows.length ? () => setShown((count) => count + PAGE_SIZE) : undefined}
        />
      )}
      <CollectiveTranscriptLibrary
        key={user?.id ?? "anonymous"}
        groupID={id}
        transcripts={readable}
        canRead={canRead}
        isOwner={isOwner}
      />
    </div>
  );

  const tabs: CollectiveTab<CollectiveTabId>[] = offeredTabs.map((def) => {
    switch (def.id) {
      case "transcripts":
        return { id: def.id, label: def.label, count: canRead ? total : undefined, panel: transcriptsPanel };
      case "waiting":
        return {
          id: def.id,
          label: def.label,
          count: pending.data?.length,
          panel: <CollectiveWaiting collectiveId={id} pending={pending} viewerId={user?.id} />,
        };
      case "members":
        return {
          id: def.id,
          label: def.label,
          count: membersVisible ? members.length : undefined,
          panel: (
            <CollectiveMembers
              members={members}
              visible={membersVisible}
              manageHref={isOwner ? settingsSectionHref(id, "members") : undefined}
            />
          ),
        };
      case "stats":
        return {
          id: def.id,
          label: def.label,
          panel: (
            <CollectiveStats
              name={group.name}
              stats={collectiveStats(stats, members.length)}
              github={github}
              onManage={github.canManage ? () => setPickerOpen(true) : undefined}
            />
          ),
        };
    }
  });

  return (
    <OnPage tight testId="collective-page">
      <OnCrumb items={[CRUMB_ROOT, { label: group.name }]} />
      <OnHead
        title={<span className="on-keep-case">{group.name}</span>}
        lede={group.description || undefined}
        actions={
          actions.length > 0 ? (
            <div className="on-cactions" data-testid="collective-header-actions">
              {actions}
            </div>
          ) : undefined
        }
      />
      {notice && (
        <div data-testid="collective-notice">
          <Toast variant={notice.ok ? "ok" : "err"} title={notice.title} onClose={() => setNotice(null)}>
            {notice.detail}
          </Toast>
        </div>
      )}
      <PolicyChips policy={policy} />
      <CollectiveTabs tabs={tabs} selected={tab} onSelect={selectTab} label="collective sections" />

      {github.canManage && (
        <RepoPicker
          open={pickerOpen}
          onClose={() => setPickerOpen(false)}
          owners={github.owners}
          initialSelected={github.linkedIds}
          title={`link repositories from ${linkedOrg ?? "github"}`}
          description={`pull requests in linked repos show the transcripts published to ${group.name}.`}
          onSave={(diff) => {
            setPickerOpen(false);
            void handleLinkRepos(diff);
          }}
        />
      )}

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
    </OnPage>
  );
}
