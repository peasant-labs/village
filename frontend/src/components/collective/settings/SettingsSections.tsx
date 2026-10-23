"use client";

import { useState, type FormEvent, type ReactNode } from "react";
import Link from "next/link";
import {
  Building2,
  DoorOpen,
  Eye,
  Github,
  Search,
  ShieldCheck,
  Trash2,
  UserMinus,
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

/**
 * A section's one card: fairtrade's setting group frame, open, with no
 * summary of its own because the page title already names the section, as the
 * local app draws a section that holds one group.
 */
function Panel({ testId, children }: { testId: string; children: ReactNode }) {
  return (
    <div className="srow-group on-settings-panel" data-testid={testId}>
      <div className="srow-rows">{children}</div>
    </div>
  );
}

// ── general ──────────────────────────────────────────────────────────────────

export function GeneralSection({ group, commit }: { group: Group; commit: Commit }) {
  return (
    <Panel testId="settings-general">
      <SettingRow label="name" control="text" value={group.name} onCommit={(next) => commit("name", next).then(() => undefined)} />
      <SettingRow
        label="description"
        control="text"
        value={group.description ?? ""}
        onCommit={(next) => commit("purpose", next).then(() => undefined)}
      />
    </Panel>
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
    <Panel testId="settings-members">
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
                <button
                  type="button"
                  className="btn btn-ghost on-mem-remove"
                  aria-label={`remove ${at}`}
                  title={`remove ${at}`}
                  onClick={() => onRemove(member)}
                >
                  <UserMinus size={16} aria-hidden="true" />
                  <span className="on-mem-remove-word">remove</span>
                </button>
              )}
            </li>
          );
        })}
      </ul>

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
    </Panel>
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
      <Panel testId="settings-github">
        <div className="srow">
          <span className="srow-help">github connection isn&apos;t set up on this server.</span>
        </div>
      </Panel>
    );
  }
  const org = github.org;
  return (
    <Panel testId="settings-github">
      <div className="srow on-row-stack" data-testid="settings-org-card">
        <span className="srow-text-col">
          <span className="srow-label on-org-label">
            <Github aria-hidden="true" />
            <span className="normal-case">{org ? org.login : "no org linked"}</span>
          </span>
          <span className="srow-help">
            {org
              ? `${typeof org.total === "number" ? `${org.linked ?? 0} of ${org.total} repositories linked. ` : ""}pull requests in these repositories can show this collective's sessions.`
              : "connect github to link an organization's repositories, so their pull requests can show this collective's sessions."}
          </span>
        </span>
        {(onManage || onConnect) && (
          <span className="srow-control">
            {onManage && (
              <button type="button" className="btn btn-secondary btn-sm" onClick={onManage}>
                manage repositories
              </button>
            )}
            {onConnect && (
              <button type="button" className="btn btn-secondary btn-sm" onClick={onConnect}>
                <Github aria-hidden="true" /> connect github
              </button>
            )}
          </span>
        )}
      </div>
    </Panel>
  );
}

// ── pull requests ────────────────────────────────────────────────────────────

export function PullRequestsSection({ group, commit }: { group: Group; commit: Commit }) {
  return (
    <Panel testId="settings-pull-requests">
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
    </Panel>
  );
}

// ── danger zone ──────────────────────────────────────────────────────────────

export function DangerSection({ group, onDelete }: { group: Group; onDelete: () => void }) {
  return (
    <Panel testId="settings-danger">
      <div className="srow on-row-stack">
        <span className="srow-text-col">
          <span className="srow-label">
            delete <span className="on-keep-case">{group.name}</span>
          </span>
          <span className="srow-help">
            this removes the collective and every member&apos;s access through it. it cannot be undone.
          </span>
        </span>
        <span className="srow-control">
          <button type="button" className="btn btn-danger btn-sm" onClick={onDelete}>
            <Trash2 aria-hidden="true" /> delete collective
          </button>
        </span>
      </div>
    </Panel>
  );
}
