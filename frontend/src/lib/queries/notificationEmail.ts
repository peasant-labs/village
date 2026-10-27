import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { VillageStatusResponse, VillageUserSettings } from "@peasant-labs/schema";
import { api } from "../api";
import { userPromptSettingsKey } from "./pulls";

/**
 * The signed-in person's notification email: one address Village may email
 * when a collective invites them. Every write answers the caller's whole
 * settings, so the cached settings are replaced with the server's answer and
 * nothing is computed here. The address appears in no other response.
 */

function useSettingsWrite<TVariables>(username: string, request: (variables: TVariables) => Promise<VillageUserSettings>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: request,
    onSuccess: (settings) => {
      qc.setQueryData(userPromptSettingsKey(username), settings);
    },
  });
}

/** Sets or replaces the address and has Village email a verification code to it. 501: this Village sends no email. */
export function useSetNotificationEmail(username: string) {
  return useSettingsWrite(username, (address: string) =>
    api<VillageUserSettings>("/users/me/email", { method: "PUT", body: JSON.stringify({ address }) }),
  );
}

/** Confirms the address with the code Village emailed to it. */
export function useVerifyNotificationEmail(username: string) {
  return useSettingsWrite(username, (token: string) =>
    api<VillageUserSettings>("/users/me/email/verify", { method: "POST", body: JSON.stringify({ token }) }),
  );
}

/** Removes the address. */
export function useRemoveNotificationEmail(username: string) {
  return useSettingsWrite(username, () => api<VillageUserSettings>("/users/me/email", { method: "DELETE" }));
}

/** Turns invitation emails on or off (`email_invitations` on `PATCH /users/me/settings`). */
export function useUpdateEmailInvitations(username: string) {
  return useSettingsWrite(username, (on: boolean) =>
    api<VillageUserSettings>("/users/me/settings", { method: "PATCH", body: JSON.stringify({ email_invitations: on }) }),
  );
}

/**
 * The signed-out one-click unsubscribe an invitation email links to. It is
 * sent only when the person presses the button, never on arrival, because mail
 * scanners open links.
 */
export function useUnsubscribeInvitationEmails() {
  return useMutation({
    mutationFn: (token: string) =>
      api<VillageStatusResponse>(`/email/unsubscribe?token=${encodeURIComponent(token)}`, { method: "POST" }),
  });
}
