"use client";

import { use, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import {
  Github,
  GitPullRequest,
  KeyRound,
  Lock,
  SlidersHorizontal,
  Trash2,
  TriangleAlert,
  UserMinus,
  Users,
} from "lucide-react";
import { ConsentDialog, Dialog, RepoPicker, SettingsLayout, Toast } from "@/lib/ft-ui";
import { OnCrumb, OnEmpty, OnPage } from "@/components/online/Page";
import {
  useAddGroupMember,
  useDeleteGroup,
  useGroup,
  usePromoteMember,
  useRemoveGroupMember,
  useUpdateGroup,
} from "@/lib/queries/groups";
import { collectiveMembers, settingPatch, type CollectiveSettingKey } from "@/lib/adapters/collective";
import {
  INSTALLED_PARAM,
  SETTINGS_SECTIONS,
  settingsSectionFrom,
  settingsSectionHref,
  type SettingsSectionId,
} from "@/lib/collectiveSections";
import { githubInstallURL } from "@/lib/githubInstall";
import { isApiErrorStatus } from "@/lib/api";
import GitHubUserSearch from "@/components/GitHubUserSearch";
import { CollectiveNotFound, CollectiveReadError, CollectiveSkeleton } from "@/components/collective/page/CollectiveStates";
import { useCollectiveGithub } from "@/components/collective/page/useCollectiveGithub";
import {
  AccessSection,
  DangerSection,
  GeneralSection,
  GithubSection,
  MembersSection,
  PullRequestsSection,
} from "@/components/collective/settings/SettingsSections";
import { ownerHandle } from "@/lib/format";
import { publicProfile } from "@/lib/publicOwner";
import type { GroupMember } from "@/lib/types";
import "@/app/online-collective.css";

/** One line the page tells the owner after an action: saved, or what went wrong. */
interface Notice {
  ok: boolean;
  title: string;
  detail: string;
}

const SECTION_ICONS: Record<SettingsSectionId, typeof Users> = {
  general: SlidersHorizontal,
  access: KeyRound,
  members: Users,
  github: Github,
  "pull-requests": GitPullRequest,
  danger: TriangleAlert,
};

const CRUMB_ROOT = { label: "collectives", href: "/collectives" };

export default function GroupSettingsPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const searchParams = useSearchParams();
  const qc = useQueryClient();
  const collective = useGroup(id);
  const { data } = collective;
  const updateGroup = useUpdateGroup();
  const deleteGroup = useDeleteGroup();
  const promoteMember = usePromoteMember();
  const removeMember = useRemoveGroupMember();
  const addMember = useAddGroupMember();

  const isOwner = data?.your_role === "owner";
  const linkedOrg = data?.group.linked_github_org ?? null;
  const github = useCollectiveGithub(id, { isOwner, canReadLinks: isOwner, linkedOrg });
  const section = settingsSectionFrom(searchParams);

  const [pickerOpen, setPickerOpen] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteHandle, setInviteHandle] = useState("");
  const [notice, setNotice] = useState<Notice | null>(null);
  const [removing, setRemoving] = useState<GroupMember | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  // A failed role change leaves the member's select on the value that did not
  // save, so the list is drawn again from the server's value.
  const [membersVersion, setMembersVersion] = useState(0);

  // The install handshake returns here with `?github_installed=1`: say so,
  // read the collective and its repositories again, and land on the github
  // section without the parameter, so a reload does not repeat it.
  const installedHandled = useRef(false);
  const installed = searchParams.get(INSTALLED_PARAM) === "1";
  useEffect(() => {
    if (!installed || installedHandled.current) return;
    installedHandled.current = true;
    setNotice({
      ok: true,
      title: "github connected",
      detail: "the github app is installed. pick the repositories to link.",
    });
    void qc.invalidateQueries({ queryKey: ["group", id] });
    void qc.invalidateQueries({ queryKey: ["group-repositories", id] });
    void qc.invalidateQueries({ queryKey: ["group-repositories-available", id] });
    router.replace(settingsSectionHref(id, "github"));
  }, [installed, id, qc, router]);

  // Pending covers the wait for the session too: the read is not sent until the
  // viewer is known, and "not found" must never show before it has answered.
  // The page's title is the section, known from the address before the read.
  if (collective.isPending) return <CollectiveSkeleton crumb={[CRUMB_ROOT, { label: "settings" }]} title={section.label} />;

  // A 404 is the friendly not-found state below; other failures say what broke.
  if (collective.isError && !isApiErrorStatus(collective.error, 404)) {
    return (
      <CollectiveReadError
        crumb={[CRUMB_ROOT, { label: "settings" }]}
        title={section.label}
        message={collective.error.message}
        retrying={collective.isFetching}
        onRetry={() => void collective.refetch()}
      />
    );
  }

  if (!data) return <CollectiveNotFound />;

  const { group } = data;
  const crumb = [CRUMB_ROOT, { label: group.name, href: `/collectives/${id}` }, { label: "settings" }];

  if (!isOwner) {
    return (
      <OnPage tight testId="collective-settings-not-owner">
        <OnCrumb items={crumb} />
        <OnEmpty
          icon={<Lock />}
          title="only the owner can change these settings"
          headingLevel={1}
          body="ask the collective's owner if something here needs to change."
          actions={
            <Link href={`/collectives/${id}`} className="btn btn-secondary">
              back to the collective
            </Link>
          }
        />
      </OnPage>
    );
  }

  const members = collectiveMembers(data.members ?? []);

  // One field per change; the server keeps every field the body leaves out.
  // A rejected promise is what tells the control to put the old value back.
  const commit = (key: CollectiveSettingKey, value: unknown) =>
    updateGroup.mutateAsync({ id, body: settingPatch(key, value) });

  const handleRoleChange = (member: GroupMember, role: string) => {
    const profile = publicProfile(member.member);
    if (!profile) return;
    const handle = `@${profile.username}`;
    promoteMember.mutate(
      { groupId: id, userId: profile.id, role },
      {
        onSuccess: () => setNotice({ ok: true, title: "role changed", detail: `${handle} is now a ${role}.` }),
        onError: (error) => {
          setMembersVersion((version) => version + 1);
          setNotice({ ok: false, title: "role not changed", detail: `${handle}: ${error.message}` });
        },
      },
    );
  };

  const handleInvite = (handle: string): Promise<boolean> => {
    const username = handle.trim().replace(/^@/, "");
    if (!username) return Promise.resolve(false);
    return new Promise((resolve) =>
      addMember.mutate(
        { groupId: id, username },
        {
          onSuccess: () => {
            setInviteOpen(false);
            setNotice({ ok: true, title: "member added", detail: `@${username} can now publish here.` });
            resolve(true);
          },
          onError: (error) => {
            setNotice({ ok: false, title: "could not add a member", detail: `@${username}: ${error.message}` });
            resolve(false);
          },
        },
      ),
    );
  };

  const handleLinkRepos = async (diff: { add: string[]; remove: string[] }) => {
    setNotice(await github.saveLinks(diff));
  };

  const connect = () => window.location.assign(githubInstallURL(id));

  let body;
  switch (section.id) {
    case "general":
      body = <GeneralSection group={group} commit={commit} />;
      break;
    case "access":
      body = <AccessSection group={group} commit={commit} />;
      break;
    case "members":
      body = (
        <MembersSection
          key={membersVersion}
          group={group}
          members={members}
          inviting={addMember.isPending && !inviteOpen}
          onInvite={handleInvite}
          onOpenSearch={() => {
            setInviteHandle("");
            addMember.reset();
            setInviteOpen(true);
          }}
          onRoleChange={handleRoleChange}
          onRemove={setRemoving}
          commit={commit}
        />
      );
      break;
    case "github":
      body = (
        <GithubSection
          github={github}
          onManage={github.canManage ? () => setPickerOpen(true) : undefined}
          onConnect={github.needsInstall ? connect : undefined}
        />
      );
      break;
    case "pull-requests":
      body = <PullRequestsSection group={group} commit={commit} />;
      break;
    case "danger":
      body = <DangerSection group={group} onDelete={() => setConfirmingDelete(true)} />;
      break;
  }

  return (
    <OnPage tight testId="collective-settings">
      <OnCrumb items={crumb} />
      <header className="on-head">
        <h1 className="on-title">{section.label}</h1>
      </header>
      {notice && (
        <div data-testid="collective-notice">
          <Toast variant={notice.ok ? "ok" : "err"} title={notice.title} onClose={() => setNotice(null)}>
            {notice.detail}
          </Toast>
        </div>
      )}
      <SettingsLayout
        sections={SETTINGS_SECTIONS.map((entry) => ({
          id: entry.id,
          label: entry.label,
          href: settingsSectionHref(id, entry.id),
          icon: SECTION_ICONS[entry.id],
        }))}
        currentId={section.id}
        LinkComponent={Link}
        bodyClassName="on-settings-body"
      >
        {body}
      </SettingsLayout>

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

      <Dialog
        open={inviteOpen}
        onClose={() => setInviteOpen(false)}
        title="invite a github user"
        dismissible={!addMember.isPending}
        footer={
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            disabled={!inviteHandle.trim() || addMember.isPending}
            onClick={() => void handleInvite(inviteHandle)}
          >
            {addMember.isPending ? "inviting" : "invite"}
          </button>
        }
      >
        <p className="text-[length:var(--fs-body)] text-ink-2">find their github handle, then invite them to this collective.</p>
        <GitHubUserSearch value={inviteHandle} onChange={setInviteHandle} onSelect={setInviteHandle} />
        {addMember.isError && (
          <p role="alert" className="text-[length:var(--fs-body)] text-danger">
            could not add a member: {addMember.error.message}. try again.
          </p>
        )}
      </Dialog>

      <ConsentDialog
        open={removing !== null}
        labelId="cns-remove-member"
        tone="danger"
        title={
          <>
            remove <span className="cns-name">{ownerHandle(removing?.member)}</span>?
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
          const profile = publicProfile(removing?.member);
          if (!profile) return;
          const handle = `@${profile.username}`;
          removeMember.mutate(
            { groupId: id, userId: profile.id },
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
            {members.length === 1 ? "its one member loses" : `all ${members.length} members lose`} access through this
            collective, and its settings are gone for good. published transcripts stay with their authors.
          </p>
        }
        consentLabel="i understand this cannot be undone"
        confirmLabel="delete collective"
        confirmIcon={Trash2}
        busy={deleteGroup.isPending}
        onCancel={() => setConfirmingDelete(false)}
        onConfirm={() =>
          deleteGroup.mutate(id, {
            onSuccess: () => router.push("/collectives"),
            onError: (error) => {
              setConfirmingDelete(false);
              setNotice({ ok: false, title: "collective not deleted", detail: error.message });
            },
          })
        }
      />
    </OnPage>
  );
}
