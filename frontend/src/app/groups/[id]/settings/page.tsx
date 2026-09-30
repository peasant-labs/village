"use client";

import { use, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Github, Lock, Trash2, UserMinus, Users } from "lucide-react";
import { CollectiveSettingsView } from "@peasant-labs/fairtrade/commons";
import { ConsentDialog, Toast } from "@/lib/ft-ui";
import {
  useAddGroupMember,
  useDeleteGroup,
  useGroup,
  usePromoteMember,
  useRemoveGroupMember,
  useUpdateGroup,
} from "@/lib/queries/groups";
import {
  isNotConfigured,
  useAvailableRepositories,
  useLinkRepository,
  useRepositories,
  useUnlinkRepository,
} from "@/lib/queries/repositories";
import {
  collectiveMembers,
  linkedRepoIds,
  MEMBER_LEAVES_CHOICES,
  memberByHandle,
  orgSummary,
  repoPickerOwners,
  settingPatch,
  settingsCollective,
  settingsMember,
  whoCanPublishChoices,
  whoCanReadChoices,
  type CollectiveSettingKey,
} from "@/lib/adapters/collective";
import { applyRepoLinks, repoLinkMessage, repoLinkSteps, splitRepo } from "@/lib/repoLinks";
import { githubInstallURL } from "@/lib/githubInstall";
import type { GroupMember } from "@/lib/types";

/** One line the page tells the owner after an action: saved, or what went wrong. */
interface Notice {
  ok: boolean;
  title: string;
  detail: string;
}

/** The query the GitHub App install handshake returns to this page with. */
const INSTALLED_PARAM = "github_installed";

export default function GroupSettingsPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const searchParams = useSearchParams();
  const qc = useQueryClient();
  const { data, isLoading } = useGroup(id);
  const updateGroup = useUpdateGroup();
  const deleteGroup = useDeleteGroup();
  const promoteMember = usePromoteMember();
  const removeMember = useRemoveGroupMember();
  const addMember = useAddGroupMember();
  const linkRepository = useLinkRepository();
  const unlinkRepository = useUnlinkRepository();

  const isOwner = data?.your_role === "owner";
  const linkedOrg = data?.group.linked_github_org ?? null;
  const repositories = useRepositories(id, isOwner);
  const available = useAvailableRepositories(id, isOwner && !!linkedOrg);

  const [notice, setNotice] = useState<Notice | null>(null);
  const [removing, setRemoving] = useState<GroupMember | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  // A failed role change leaves the member's select on the value that did not
  // save, so the list is drawn again from the server's value.
  const [membersVersion, setMembersVersion] = useState(0);

  // The install handshake returns here with `?github_installed=1`: say so,
  // read the collective and its repositories again, and drop the parameter so
  // a reload does not repeat it.
  const installedHandled = useRef(false);
  const installed = searchParams.get(INSTALLED_PARAM) === "1";
  useEffect(() => {
    if (!installed || installedHandled.current) return;
    installedHandled.current = true;
    setNotice({
      ok: true,
      title: "github connected",
      detail: "the github app is installed. pick the repositories to link under github orgs.",
    });
    void qc.invalidateQueries({ queryKey: ["group", id] });
    void qc.invalidateQueries({ queryKey: ["group-repositories", id] });
    void qc.invalidateQueries({ queryKey: ["group-repositories-available", id] });
    router.replace(`/groups/${id}/settings`);
  }, [installed, id, qc, router]);

  if (isLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="h-4 w-56 bg-surface-hover animate-shimmer" />
        <div className="h-16 w-72 bg-surface-hover animate-shimmer" />
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
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

  const { group } = data;

  if (!isOwner) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <Lock size={28} className="text-ink-4" aria-hidden="true" />
          <p className="text-sm font-medium text-ink">only the owner can change these settings</p>
          <Link
            href={`/groups/${id}`}
            className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            back to the collective
          </Link>
        </div>
      </div>
    );
  }

  const members = collectiveMembers(data.members ?? []);
  const githubConfigured = !(repositories.isError && isNotConfigured(repositories.error));
  const linked = repositories.data?.repositories ?? [];
  const offered = available.data?.repositories ?? [];
  const owners = repoPickerOwners(offered, linked, linkedOrg);
  const canManageRepos = !!linkedOrg && githubConfigured && repositories.isSuccess;
  const needsInstall =
    githubConfigured &&
    repositories.isSuccess &&
    (!linkedOrg || (available.isSuccess && offered.length === 0 && linked.length === 0));

  // One field per change; the server keeps every field the body leaves out.
  // A rejected promise is what tells the row to put the old value back.
  const commit = (key: CollectiveSettingKey, value: unknown) =>
    updateGroup.mutateAsync({ id, body: settingPatch(key, value) });

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

  const handleRoleChange = (handle: string, role: string) => {
    const member = memberByHandle(members, handle);
    if (!member) return;
    promoteMember.mutate(
      { groupId: id, userId: member.id, role },
      {
        onSuccess: () => setNotice({ ok: true, title: "role changed", detail: `${handle} is now a ${role}.` }),
        onError: (error) => {
          setMembersVersion((version) => version + 1);
          setNotice({ ok: false, title: "role not changed", detail: `${handle}: ${error.message}` });
        },
      },
    );
  };

  const handleInvite = (handle: string) => {
    const username = handle.trim().replace(/^@/, "");
    if (!username) return;
    addMember.mutate(
      { groupId: id, username },
      {
        onSuccess: () => setNotice({ ok: true, title: "member added", detail: `@${username} can now publish here.` }),
        onError: (error) => setNotice({ ok: false, title: "could not add a member", detail: `@${username}: ${error.message}` }),
      },
    );
  };

  const hrefFor = (kind: string, target?: string): string | undefined => {
    switch (kind) {
      case "collectives":
        return "/groups";
      case "collective":
        return target ? `/groups/${target}` : undefined;
      default:
        return undefined;
    }
  };

  return (
    <div className="cmg-root max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-4 animate-fade-up">
      {/* With no github org to manage, the way forward is the install
          handshake. The view names no such action, so the page draws it on the
          breadcrumb's line: static above it on a small screen, beside it from md
          up. The box is the view's own content column (fairtrade's .iu-page:
          1184px wide, 24px above and 16px beside its content), so the action
          lines up with the breadcrumb. */}
      <div className="relative mx-auto w-full max-w-[1184px]">
        {needsInstall && (
          <div
            className="flex flex-wrap items-center justify-end gap-2 pt-3 md:absolute md:right-[var(--sp-4)] md:top-[calc(var(--sp-5)-4px)] md:z-[1] md:pt-0"
            data-testid="collective-header-actions"
          >
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => window.location.assign(githubInstallURL(id))}
            >
              <Github size={14} aria-hidden="true" /> connect github
            </button>
          </div>
        )}
        {notice && (
          <div className="px-[var(--sp-4)] pt-[var(--sp-5)]" data-testid="collective-notice">
            <Toast variant={notice.ok ? "ok" : "err"} title={notice.title} onClose={() => setNotice(null)}>
              {notice.detail}
            </Toast>
          </div>
        )}
        <CollectiveSettingsView
          key={membersVersion}
          data={{
            collective: {
              ...settingsCollective(group),
              org: orgSummary(linkedOrg, repositories.data ? linked.length : undefined, available.data ? offered.length : undefined),
              members: members.map(settingsMember),
              memberCount: members.length,
            },
            owners,
            linkedRepos: linkedRepoIds(linked, owners),
            showOnPullRequests: group.post_prompts_check ?? false,
            whoCanPublish: whoCanPublishChoices(linkedOrg),
            whoCanRead: whoCanReadChoices(group.data_access),
            memberLeavesChoices: MEMBER_LEAVES_CHOICES,
            hrefFor,
          }}
          actions={{
            onCommit: commit,
            onInvite: handleInvite,
            onRoleChange: handleRoleChange,
            onRemoveMember: (handle: string) => setRemoving(memberByHandle(members, handle) ?? null),
            onLinkRepos: canManageRepos ? (diff) => void handleLinkRepos(diff) : undefined,
            onDelete: () => setConfirmingDelete(true),
          }}
        />
      </div>

      <ConsentDialog
        open={removing !== null}
        labelId="cns-remove-member"
        tone="danger"
        title={
          <>
            remove <span className="cns-name">@{removing?.github_username}</span>?
          </>
        }
        intro={
          <p>
            they stop being a member of <span className="cns-name">{group.name}</span>. what happens to the
            transcripts they published here follows when a member leaves.
          </p>
        }
        requireConsent={false}
        confirmLabel="remove"
        confirmIcon={UserMinus}
        busy={removeMember.isPending}
        onCancel={() => setRemoving(null)}
        onConfirm={() => {
          if (!removing) return;
          const handle = `@${removing.github_username}`;
          removeMember.mutate(
            { groupId: id, userId: removing.id },
            {
              onSuccess: () => {
                setRemoving(null);
                setNotice({ ok: true, title: "member removed", detail: `${handle} is no longer a member.` });
              },
              onError: (error) => {
                setRemoving(null);
                setNotice({ ok: false, title: "member not removed", detail: `${handle}: ${error.message}` });
              },
            },
          );
        }}
      />

      <ConsentDialog
        open={confirmingDelete}
        labelId="cns-delete-collective"
        tone="danger"
        title={
          <>
            delete <span className="cns-name">{group.name}</span>?
          </>
        }
        intro={
          <p>
            the collective and all of its membership are deleted. published transcripts stay with their authors.
            this cannot be undone.
          </p>
        }
        consentLabel="i understand this cannot be undone"
        confirmLabel="delete collective"
        confirmIcon={Trash2}
        busy={deleteGroup.isPending}
        onCancel={() => setConfirmingDelete(false)}
        onConfirm={() =>
          deleteGroup.mutate(id, {
            onSuccess: () => router.push("/groups"),
            onError: (error) => {
              setConfirmingDelete(false);
              setNotice({ ok: false, title: "collective not deleted", detail: error.message });
            },
          })
        }
      />
    </div>
  );
}
