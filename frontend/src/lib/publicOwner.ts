import type { VillagePublicOwner } from "@peasant-labs/schema";

/**
 * A person as another viewer sees them: their public profile, or anonymous.
 *
 * The server decides which one each viewer receives. This app renders what it
 * receives and never re-derives the rule: there is no discoverability flag on
 * the wire to read, and an anonymous owner carries no id, handle or avatar.
 */
export type PublicOwner = VillagePublicOwner;

/** The profile arm of {@link PublicOwner}. */
export type PublicProfile = Extract<VillagePublicOwner, { kind: "profile" }>;

/** An owner this viewer may not identify. */
export const ANONYMOUS_OWNER: PublicOwner = Object.freeze({ kind: "anonymous" }) as PublicOwner;

/**
 * The profile `owner` carries, or `null` when it names nobody.
 *
 * Responses are read with `JSON.parse` and not run through the contract's
 * parser, so this is where an unknown `kind` is read as anonymous: only a
 * `profile` with a usable id and username is a profile, and nothing is kept
 * from any other shape. A missing owner is anonymous too.
 */
export function publicProfile(owner: PublicOwner | null | undefined): PublicProfile | null {
  if (!owner || owner.kind !== "profile") return null;
  const candidate = owner as Partial<PublicProfile>;
  if (typeof candidate.id !== "string" || candidate.id === "") return null;
  if (typeof candidate.username !== "string" || candidate.username === "") return null;
  return owner;
}

/**
 * Whether `owner` is the signed-in viewer: a profile whose id is the viewer's.
 *
 * An anonymous owner is never the viewer, because the server always shows a
 * viewer their own profile.
 */
export function isOwnedBy(owner: PublicOwner | null | undefined, viewerId: string | null | undefined): boolean {
  if (!viewerId) return false;
  return publicProfile(owner)?.id === viewerId;
}

/**
 * A key that tells profiles apart and puts every anonymous owner in one
 * bucket. Counting or filtering by owner uses it, because anonymous owners
 * carry no id that could tell them apart.
 */
export const ANONYMOUS_OWNER_KEY = "anonymous";

export function ownerKey(owner: PublicOwner | null | undefined): string {
  return publicProfile(owner)?.id ?? ANONYMOUS_OWNER_KEY;
}
