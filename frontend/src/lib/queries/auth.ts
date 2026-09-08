import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, getAuthHeaders, isApiErrorStatus } from "../api";
import type { User } from "../types";

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async ({ signal }) => {
      const credentials = getAuthHeaders().Authorization;
      const assertCurrentCredentials = () => {
        if (getAuthHeaders().Authorization !== credentials) {
          throw new Error("Authentication credentials changed while checking /auth/me. The identity response was withheld. Retry authentication to check the current session.");
        }
      };
      try {
        const user = await api<User>("/auth/me", { signal });
        assertCurrentCredentials();
        return user;
      } catch (error) {
        if (!isApiErrorStatus(error, 401)) throw error;
        // An expired credential must not accompany confirmed-anonymous reads.
        // Do not erase a newer credential installed while this request ran.
        assertCurrentCredentials();
        document.cookie = "peasant_token=; path=/; max-age=0";
        return null;
      }
    },
    retry: false,
  });
}

export function usePublicProfile(username: string) {
  return useQuery({
    queryKey: ["user", username],
    queryFn: () => api<User>(`/users/${encodeURIComponent(username)}`),
    retry: false,
    enabled: !!username,
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api("/auth/logout", { method: "POST" }),
    onSuccess: () => {
      document.cookie = "peasant_token=; path=/; max-age=0";
      qc.clear();
      window.location.href = "/";
    },
  });
}

export function useDeleteAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api("/auth/me", { method: "DELETE" }),
    onSuccess: () => {
      document.cookie = "peasant_token=; path=/; max-age=0";
      qc.clear();
      window.location.href = "/";
    },
  });
}

export function useSetUsername() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (username: string) =>
      api<User>("/auth/me/username", {
        method: "PATCH",
        body: JSON.stringify({ username }),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["me"], updated);
    },
  });
}

export function useUpdateMySettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { is_discoverable: boolean }) =>
      api<User>("/auth/me/settings", {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["me"], updated);
      qc.invalidateQueries({ queryKey: ["transcripts"] });
      qc.invalidateQueries({ queryKey: ["group"] });
    },
  });
}
