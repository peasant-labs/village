import type { VillageCollectiveInvitation, VillagePublicOwner } from "@peasant-labs/schema";
import { isApiErrorStatus } from "./api";
import { publicProfile } from "./publicOwner";

/**
 * The words the invitation surfaces share. The server decides every outcome;
 * these lines only restate it. Inviting answers the same way whether or not an
 * account holds the name, so the copy says so instead of claiming the person
 * was found.
 */

/** What the owner is told after any invitation the server took. */
export function invitedDetail(username: string): string {
  return `if @${username} has a village account they'll see it. we don't say whether they do.`;
}

/**
 * Why an invitation was not sent, in the owner's words. Each status the invite
 * route declares has its own lowercase line; the server's sentence-case
 * message is never spliced into the chrome. Anything else, a dropped
 * connection included, is one generic line.
 */
export function inviteRefusal(username: string, error: unknown): string {
  const at = username ? `@${username}` : "they";
  if (isApiErrorStatus(error, 409)) return `${at} is already a member of this collective.`;
  if (isApiErrorStatus(error, 429)) {
    return "you've reached the invitation limit. revoke an open invitation or try again tomorrow.";
  }
  if (isApiErrorStatus(error, 400)) return "type a username of 1 to 100 characters with no spaces.";
  if (isApiErrorStatus(error, 401)) return "your session has ended. sign in again to invite people.";
  if (isApiErrorStatus(error, 403)) return "only an owner of this collective can invite people.";
  return `${at} was not invited. try again.`;
}

/** Who sent an invitation, as the invitee may see them: a handle, or "an owner". */
export function invitedByLabel(owner: VillagePublicOwner): string {
  const profile = publicProfile(owner);
  return profile ? `@${profile.username}` : "an owner";
}

/**
 * The accept dialog's opening line, in parts so the handle or the collective's
 * name can be drawn as a name (never lowercased): `@owner invited you.`, or
 * `an owner of {collective} invited you.` when the inviter is hidden.
 */
export function invitedYouParts(
  invitation: Pick<VillageCollectiveInvitation, "invited_by" | "collective">,
): { before: string; name: string; after: string } {
  const profile = publicProfile(invitation.invited_by);
  return profile
    ? { before: "", name: `@${profile.username}`, after: " invited you." }
    : { before: "an owner of ", name: invitation.collective.name, after: " invited you." };
}
