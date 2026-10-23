/**
 * The words the collectives list, the discover page and the new-collective
 * dialog use for a collective's two policies. Each restates what the server
 * decides (`acceptance_mode`, `data_access`, the linked org); none of them
 * decides anything, so a change to the server's rule moves this copy with it.
 */

/** The three ways a collective takes members and transcripts, as the wire names them. */
export type AcceptanceMode = "open" | "verified_only" | "curated";

/** The two read policies a new collective can be given. `public` is not offered. */
export type NewCollectiveAccess = "members_only" | "contributors";

/**
 * Who can publish, in the few words a table cell holds.
 *
 * A curated collective's owner approves each transcript, so its owner reads
 * "sessions you approve" and everybody else "sessions the owner approves". An
 * unknown mode says nothing rather than guessing.
 */
export function whoCanPublishShort(mode: string, linkedOrg: string | null, viewerIsOwner: boolean): string {
  switch (mode) {
    case "open":
      return "anyone who joins";
    case "verified_only":
      return linkedOrg ? `${linkedOrg} org members` : "github org members";
    case "curated":
      return viewerIsOwner ? "sessions you approve" : "sessions the owner approves";
    default:
      return "";
  }
}

/**
 * Who can join, on a discover card. A verified collective with no linked org
 * takes anybody the server knows to be in some GitHub org, so it says that
 * rather than naming one.
 */
export function whoCanJoin(mode: string, linkedOrg: string | null): string {
  switch (mode) {
    case "open":
      return "anyone can join and publish";
    case "verified_only":
      return linkedOrg ? `members of the ${linkedOrg} github org` : "members of any github org";
    case "curated":
      return "invite only; each session approved";
    default:
      return "";
  }
}

/** What happens next in a collective just created, for the dialog's last step. */
export function readyLine(mode: AcceptanceMode, linkedOrg: string | null): string {
  switch (mode) {
    case "verified_only":
      return linkedOrg
        ? `members of the ${linkedOrg} github org can join it and publish from peasant.`
        : "members of a github org can join it and publish from peasant.";
    case "curated":
      return "members publish to it from peasant, and you approve each session.";
    default:
      return "anyone can join it and publish from peasant.";
  }
}

/** A count with its noun, singular for one: "1 member", "4 members". */
export function counted(n: number, noun: string): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? noun : `${noun}s`}`;
}
