import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { TagWithCount } from "../types";

export function usePopularTags(limit = 10) {
  return useQuery({
    queryKey: ["popular-tags", limit],
    queryFn: () => api<TagWithCount[]>(`/tags/popular?limit=${limit}`),
  });
}
