import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { User } from "../types";

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: () => api<User>("/auth/me"),
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
