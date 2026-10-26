import type { PublicOwner, PublicProfile } from "@/lib/publicOwner";

/**
 * The public profile the server sends other viewers for an account, built
 * from the account fields a fixture already states.
 */
export function profileOwner(account: {
  id: string;
  github_username: string;
  display_name?: string | null;
  avatar_url?: string | null;
}): PublicProfile {
  return {
    kind: "profile",
    id: account.id,
    username: account.github_username,
    display_name: account.display_name ?? null,
    avatar_url: account.avatar_url ?? null,
  };
}

/** An owner the server does not name to this viewer. */
export const anonymousOwner: PublicOwner = { kind: "anonymous" };
