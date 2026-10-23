"use client";

import { useState, type FormEvent } from "react";
import Link from "next/link";
import {
  Building2,
  DoorOpen,
  Eye,
  Github,
  Search,
  ShieldCheck,
  Trash2,
  TriangleAlert,
  UserPlus,
  Users,
} from "lucide-react";
import { Avatar, SettingRow } from "@/lib/ft-ui";
import {
  MEMBER_LEAVES_CHOICES,
  whoCanPublishChoices,
  whoCanReadChoices,
  type CollectiveSettingKey,
} from "@/lib/adapters/collective";
import type { Group, GroupMember } from "@/lib/types";
import type { CollectiveGithub } from "@/components/collective/page/useCollectiveGithub";
import ChoiceCards from "./ChoiceCards";

/** The one write per change; a rejection tells the control to put the old value back. */
export type Commit = (key: CollectiveSettingKey, value: unknown) => Promise<unknown>;

// ── general ──────────────────────────────────────────────────────────────────

export function GeneralSection({ group, commit }: { group: Group; commit: Commit }) {
  return (
    <div className="on-rows" data-testid="settings-general">
      <SettingRow label="name" control="text" value={group.name} onCommit={(next) => commit("name", next).then(() => undefined)} />
      <SettingRow
        label="description"
        control="text"
        value={group.description ?? ""}
        onCommit={(next) => commit("purpose", next).then(() => undefined)}
      />
    </div>
  );
}

// ── access ───────────────────────────────────────────────────────────────────

const PUBLISH_ICONS = {
  open: <DoorOpen />,
  verified_only: <Building2 />,
  curated: <ShieldCheck />,
};

const READ_ICONS = {
  members_only: <Eye />,
  contributors: <Users />,
  public: <Eye />,
};

export function AccessSection({ group, commit }: { group: Group; commit: Commit }) {
  return (
    <div className="on-section" data-testid="settings-access" style={{ gap: "var(--sp-7)" }}>
      <ChoiceCards
        label="who can publish"
        value={group.acceptance_mode}
        choices={whoCanPublishChoices(group.linked_github_org)}
        icons={PUBLISH_ICONS}
        onCommit={(next) => commit("mode", next)}
      />
      <ChoiceCards
        label="who can read"
        value={group.data_access}
        choices={whoCanReadChoices(group.data_access)}
        icons={READ_ICONS}
        onCommit={(next) => commit("access", next)}
      />
    </div>
  );
}

// ── members ──────────────────────────────────────────────────────────────────

export function MembersSection({
  group,
  members,
  inviting,
  onInvite,
  onOpenSearch,
  onRoleChange,
  onRemove,
  commit,
}: {
  group: Group;
  members: GroupMember[];
  inviting: boolean;
  /** Invite one github username; resolves true when it was added. */
  onInvite: (username: string) => Promise<boolean>;
  onOpenSearch: () => void;
  onRoleChange: (member: GroupMember, role: string) => void;
  onRemove: (member: GroupMember) => void;
  commit: Commit;
}) {
  const [handle, setHandle] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!handle.trim()) return;
    if (await onInvite(handle)) setHandle("");
  };
  return (
    <div className="on-section" data-testid="settings-members" style={{ gap: "var(--sp-6)" }}>
      <form className="on-invite" onSubmit={(event) => void submit(event)}>
        <div className="on-field">
          <label htmlFor="collective-invite-handle">invite by github username</label>
          <input
            id="collective-invite-handle"
            className="on-input"
            value={handle}
            onChange={(event) => setHandle(event.target.value)}
            placeholder="github username"
            autoComplete="off"
            spellCheck={false}
          />
        </div>
        <button type="submit" className="btn btn-secondary" disabled={!handle.trim() || inviting}>
          <UserPlus size={16} aria-hidden="true" /> {inviting ? "inviting" : "invite"}
        </button>
        <button type="button" className="btn btn-ghost" onClick={onOpenSearch}>
          <Search size={16} aria-hidden="true" /> find a github user to invite
        </button>
      </form>

      <ul className="on-mem-list" aria-label="members">
        {members.map((member) => {
          const at = `@${member.github_username}`;
          return (
            <li key={member.id} className="on-mem">
              <span aria-hidden="true">
                <Avatar name={member.display_name || member.github_username} size="sm" src={member.avatar_url ?? undefined} />
              </span>
              <span className="on-handle">
                <span className="on-mem-login">{at}</span>
                {member.display_name && <span className="on-mem-name">{member.display_name}</span>}
              </span>
              {member.role === "owner" ? (
                <span className="on-chrome">owner</span>
              ) : (
                <span className="select-wrap">
                  <select
                    className="select"
                    aria-label={`role for ${at}`}
                    defaultValue={member.role}
                    onChange={(event) => onRoleChange(member, event.target.value)}
                  >
                    <option value="member">member</option>
                    <option value="contributor">contributor</option>
                  </select>
                </span>
              )}
              {member.role === "owner" ? (
                <span />
              ) : (
                <button type="button" className="btn btn-ghost" aria-label={`remove ${at}`} onClick={() => onRemove(member)}>
                  remove
                </button>
              )}
            </li>
          );
        })}
      </ul>

      <div className="on-rows">
        <SettingRow
          label="when a member leaves"
          help="what happens to the transcripts they published here."
          control="select"
          value={MEMBER_LEAVES_CHOICES.some((c) => c.value === group.transcript_deletion_policy) ? group.transcript_deletion_policy : ""}
          options={
            MEMBER_LEAVES_CHOICES.some((c) => c.value === group.transcript_deletion_policy)
              ? MEMBER_LEAVES_CHOICES
              : [{ value: "", label: "not set", disabled: true }, ...MEMBER_LEAVES_CHOICES]
          }
          onCommit={(next) => commit("memberLeaves", next).then(() => undefined)}
        />
      </div>
    </div>
  );
}

// ── github ───────────────────────────────────────────────────────────────────

export function GithubSection({
  github,
  onManage,
  onConnect,
}: {
  github: CollectiveGithub;
  onManage?: () => void;
  onConnect?: () => void;
}) {
  if (!github.configured) {
    return (
      <div className="on-section" data-testid="settings-github">
        <div className="on-notice">
          <p>github connection isn&apos;t set up on this server.</p>
        </div>
      </div>
    );
  }
  const org = github.org;
  return (
    <div className="on-section" data-testid="settings-github">
      <div className="on-opt on-org-card" data-testid="settings-org-card">
        <span className="on-ico-tile" aria-hidden="true">
          <Github />
        </span>
        <b>{org ? org.login : "no org linked"}</b>
        <p>
          {org
            ? `${typeof org.total === "number" ? `${org.linked ?? 0} of ${org.total} repositories linked. ` : ""}pull requests in these repositories can show this collective's sessions.`
            : "connect github to link an organization's repositories, so their pull requests can show this collective's sessions."}
        </p>
        {(onManage || onConnect) && (
          <div className="on-row">
            {onManage && (
              <button type="button" className="btn btn-secondary" onClick={onManage}>
                manage repositories
              </button>
            )}
            {onConnect && (
              <button type="button" className="btn btn-secondary" onClick={onConnect}>
                <Github size={16} aria-hidden="true" /> connect github
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// ── pull requests ────────────────────────────────────────────────────────────

export function PullRequestsSection({ group, commit }: { group: Group; commit: Commit }) {
  return (
    <div className="on-rows" data-testid="settings-pull-requests">
      <SettingRow
        label="show transcripts on pull requests"
        help="a check and one comment on each pull request. it never blocks merging."
        control="switch"
        value={group.post_prompts_check ?? false}
        onCommit={(next) => commit("showOnPullRequests", next).then(() => undefined)}
      />
      {/* Linking a person's transcripts to their pull requests is their own
          setting, not the collective's: a row that leads there. */}
      <div className="srow" data-testid="settings-auto-link-pointer">
        <span className="srow-text-col">
          <span className="srow-label">link transcripts automatically</span>
          <span className="srow-help">each member chooses it in their own settings.</span>
        </span>
        <span className="srow-control">
          <Link href="/settings#pull-requests" className="btn btn-secondary btn-sm">
            your settings
          </Link>
        </span>
      </div>
    </div>
  );
}

// ── danger zone ──────────────────────────────────────────────────────────────

export function DangerSection({ group, onDelete }: { group: Group; onDelete: () => void }) {
  return (
    <div className="on-danger" data-testid="settings-danger">
      <h2>
        <TriangleAlert size={22} aria-hidden="true" /> delete <span className="on-keep-case">{group.name}</span>
      </h2>
      <p>this removes the collective and every member&apos;s access through it. it cannot be undone.</p>
      <div>
        <button type="button" className="btn on-btn-danger" onClick={onDelete}>
          <Trash2 size={16} aria-hidden="true" /> delete collective
        </button>
      </div>
    </div>
  );
}
