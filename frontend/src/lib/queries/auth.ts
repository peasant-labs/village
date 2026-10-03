import { useQuery, useMutation, useQueryClient, type MutateOptions } from "@tanstack/react-query";
import { api, clearAuthTokenCookie, getAuthHeaders } from "../api";
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
      clearAuthTokenCookie();
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
      clearAuthTokenCookie();
      qc.clear();
      window.location.href = "/";
    },
  });
}

// Both routes return a whole profile. Serialize their writes so an older
// response cannot replace a newer value saved by the other profile control.
const PROFILE_WRITE_SCOPE = { id: "account-profile" };

type BoundProfileWrite<Value> = { value: Value; accountID: string | undefined; authorization: string | undefined };

function useProfileWrite<Value>(path: string, bodyFor: (value: Value) => object, refreshLists = false) {
  const qc = useQueryClient();
  function isCurrent(write: BoundProfileWrite<Value>) {
    return !!write.accountID && qc.getQueryData<User>(["me"])?.id === write.accountID
      && getAuthHeaders().Authorization === write.authorization;
  }
  const mutation = useMutation({
    scope: PROFILE_WRITE_SCOPE,
    mutationFn: async (write: BoundProfileWrite<Value>) => {
      // Capture identity when the person presses save, before waiting behind
      // another profile write. A later sign-in must not inherit that decision.
      if (!isCurrent(write)) throw new Error("the signed-in account changed; this profile change was not sent. review it and try again.");
      const updated = await api<User>(path, { method: "PATCH", body: JSON.stringify(bodyFor(write.value)) });
      if (!isCurrent(write)) throw new Error("the signed-in account changed while saving; reload this account before making another profile change.");
      if (updated.id !== write.accountID) throw new Error("the profile response belongs to a different account.");
      return updated;
    },
    onSuccess: (updated, write) => {
      // A request already in flight may finish after logout or account switch.
      if (!isCurrent(write)) return;
      qc.setQueryData(["me"], updated);
      if (refreshLists) {
        qc.invalidateQueries({ queryKey: ["transcripts"] });
        qc.invalidateQueries({ queryKey: ["group"] });
      }
    },
  });
  function bind(value: Value): BoundProfileWrite<Value> {
    return { value, accountID: qc.getQueryData<User>(["me"])?.id, authorization: getAuthHeaders().Authorization };
  }
  function callbacks(options?: MutateOptions<User, Error, Value, unknown>): MutateOptions<User, Error, BoundProfileWrite<Value>, unknown> | undefined {
    if (!options) return undefined;
    return {
      onSuccess: (data, write, result, context) => options.onSuccess?.(data, write.value, result, context),
      onError: (error, write, result, context) => options.onError?.(error, write.value, result, context),
      onSettled: (data, error, write, result, context) => options.onSettled?.(data, error, write.value, result, context),
    };
  }
  return {
    ...mutation,
    variables: mutation.variables?.value,
    mutate: (value: Value, options?: MutateOptions<User, Error, Value, unknown>) => mutation.mutate(bind(value), callbacks(options)),
    mutateAsync: (value: Value, options?: MutateOptions<User, Error, Value, unknown>) => mutation.mutateAsync(bind(value), callbacks(options)),
  };
}

export function useSetUsername() {
  return useProfileWrite<string>("/auth/me/username", (username) => ({ username }));
}

export function useUpdateMySettings() {
  return useProfileWrite<{ is_discoverable: boolean }>("/auth/me/settings", (body) => body, true);
}
