import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { UserOrg, UserPublicOrg } from "../types";

/**
 * Every GitHub organization the caller belongs to, linked or not.
 *
 * This is the settings-side read, so it includes organizations whose `visible`
 * flag is off: the whole point of the control is to choose among them. The
 * public profile asks a different question (`usePublicOrgs`) and gets a
 * different answer.
 */
export function useMyOrgs() {
  return useQuery({
    queryKey: ["my-orgs"],
    queryFn: () => api<UserOrg[]>("/auth/orgs"),
  });
}

/**
 * The organizations a person has linked to their profile, as any viewer sees
 * them. The server returns only the visible ones, so a page rendering these
 * never has to hide anything it was sent.
 *
 * A 404 here is the same profile-visibility boundary `usePublicProfile` reads:
 * a hidden person answers it to everyone but themselves. Retrying would ask a
 * question the server has already answered, so this read never retries.
 */
export function usePublicOrgs(username: string) {
  return useQuery({
    queryKey: ["orgs", username],
    queryFn: () => api<UserPublicOrg[]>(`/users/${encodeURIComponent(username)}/orgs`),
    enabled: !!username,
    retry: false,
  });
}

/**
 * Link or unlink one organization on the caller's own profile.
 *
 * Linking is what makes the membership public: a collective that accepts
 * verified members of a named organization reads this same flag, so this one
 * switch is both "show my org on my profile" and "let this org vouch for me".
 * A rejected write is left to the caller to report, so the row can put the
 * previous value back.
 */
export function useSetOrgVisibility() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ orgLogin, visible }: { orgLogin: string; visible: boolean }) =>
      api(`/auth/orgs/${encodeURIComponent(orgLogin)}/visibility`, {
        method: "PATCH",
        body: JSON.stringify({ visible }),
      }),
    onSuccess: () => {
      // Both reads answer "which organizations are linked": the list here and
      // the linked list the profile shows. One switch changes both.
      void queryClient.invalidateQueries({ queryKey: ["my-orgs"] });
      void queryClient.invalidateQueries({ queryKey: ["orgs"] });
    },
  });
}
