"use client";

import Link from "next/link";
import { EyeOff, UserPlus } from "lucide-react";
import { Avatar } from "@/lib/ft-ui";
import { OnEmpty } from "@/components/online/Page";
import { memberBreakdown } from "@/lib/adapters/collective";
import type { GroupMember } from "@/lib/types";

/**
 * Who belongs to the collective, owner first, with the role each holds. A
 * collective that keeps its member list private shows that instead, to
 * everyone but its owner. Changing a role or removing someone is the owner's,
 * on the settings page.
 */
export default function CollectiveMembers({
  members,
  visible,
  manageHref,
}: {
  members: GroupMember[];
  visible: boolean;
  /** The owner's way to the member settings. */
  manageHref?: string;
}) {
  if (!visible) {
    return (
      <OnEmpty
        icon={<EyeOff />}
        title="members are private"
        body="the owner keeps the member list private."
        headingLevel={3}
        testId="collective-members-private"
      />
    );
  }

  return (
    <div className="on-section">
      <div className="on-row on-members-head">
        <p className="on-muted" data-testid="collective-member-breakdown">
          {memberBreakdown(members)}
        </p>
        {manageHref && (
          <Link href={manageHref} className="btn btn-secondary">
            <UserPlus size={16} aria-hidden="true" /> manage members
          </Link>
        )}
      </div>
      <ul className="on-mem-list" aria-label="members">
        {members.map((member) => (
          <li key={member.id} className="on-mem">
            <span aria-hidden="true">
                <Avatar name={member.display_name || member.github_username} size="sm" src={member.avatar_url ?? undefined} />
              </span>
            <span className="on-handle">
              <span className="on-mem-login">@{member.github_username}</span>
              {member.display_name && <span className="on-mem-name">{member.display_name}</span>}
            </span>
            <span className="on-chrome">{member.role}</span>
            <span />
          </li>
        ))}
      </ul>
    </div>
  );
}
