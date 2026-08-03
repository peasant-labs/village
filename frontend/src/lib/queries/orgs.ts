import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { UserOrg } from "../types";

export function useMyOrgs() {
  return useQuery({
    queryKey: ["my-orgs"],
    queryFn: () => api<UserOrg[]>("/auth/orgs"),
  });
}
