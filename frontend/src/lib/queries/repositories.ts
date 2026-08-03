import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, isApiErrorStatus } from "../api";
import type {
  LinkedRepositoriesResponse,
  RepositoryCommitsResponse,
  TranscriptCommitsResponse,
} from "../types";

// HTTP 501: the GitHub App is config-gated and not registered on this server.
// All four repository endpoints report this uniformly when the feature is off,
// and we treat it as a clean "not configured" state rather than an error.
const NOT_CONFIGURED_STATUS = 501;

/** True when a query/mutation error is the backend's 501 not-configured signal. */
export function isNotConfigured(err: unknown): boolean {
  return isApiErrorStatus(err, NOT_CONFIGURED_STATUS);
}

/**
 * List the repositories linked to a collective. Readable by any member; the
 * backend returns 501 when the GitHub App is not configured. We do NOT retry
 * the 501 (it will never succeed until an admin registers the App) — callers
 * inspect the error via {@link isNotConfigured} and render an inline notice.
 */
export function useRepositories(groupId: string, enabled = true) {
  return useQuery({
    queryKey: ["group-repositories", groupId],
    queryFn: () =>
      api<LinkedRepositoriesResponse>(`/groups/${groupId}/repositories`),
    enabled: enabled && !!groupId,
    // A 501 (not configured) and a 403 (membership) are both terminal states;
    // never spin on them. Other failures get React Query's default behaviour.
    retry: (failureCount, err) => {
      if (isNotConfigured(err) || isApiErrorStatus(err, 403)) return false;
      return failureCount < 2;
    },
  });
}

/**
 * Link a GitHub repository to a collective. Owner-only (backend returns 403
 * otherwise). The backend validates the supplied installation can reach the
 * repo before persisting, so a bad owner/name/installation surfaces as a 400.
 * Invalidates the list on success.
 */
export function useLinkRepository() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      groupId,
      owner,
      name,
      installationId,
    }: {
      groupId: string;
      owner: string;
      name: string;
      installationId: number;
    }) =>
      api(`/groups/${groupId}/repositories`, {
        method: "POST",
        body: JSON.stringify({
          owner,
          name,
          installation_id: installationId,
        }),
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group-repositories", vars.groupId] });
    },
  });
}

/**
 * Unlink a repository from a collective. Owner-only. Invalidates both the list
 * and that repo's cached commits.
 */
export function useUnlinkRepository() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      groupId,
      owner,
      name,
    }: {
      groupId: string;
      owner: string;
      name: string;
    }) =>
      api(
        `/groups/${groupId}/repositories/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`,
        { method: "DELETE" }
      ),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group-repositories", vars.groupId] });
      qc.invalidateQueries({
        queryKey: ["group-repository-commits", vars.groupId, vars.owner, vars.name],
      });
    },
  });
}

/**
 * Read a linked repo's cached commits. The endpoint is cache-first; passing
 * `refresh: true` (owner-only on the backend) re-fetches from GitHub via a
 * conditional request. Reads require membership. `enabled` lets callers defer
 * the fetch until a repo's commit panel is actually opened.
 */
export function useRepositoryCommits(
  groupId: string,
  owner: string,
  name: string,
  options?: { refresh?: boolean; enabled?: boolean }
) {
  const refresh = options?.refresh ?? false;
  const enabled = options?.enabled ?? true;
  return useQuery({
    // The refresh flag is part of the key so a forced refresh is a distinct
    // fetch, but both share the same cache namespace for invalidation.
    queryKey: ["group-repository-commits", groupId, owner, name, refresh],
    queryFn: () =>
      api<RepositoryCommitsResponse>(
        `/groups/${groupId}/repositories/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/commits${
          refresh ? "?refresh=true" : ""
        }`
      ),
    enabled: enabled && !!groupId && !!owner && !!name,
    retry: (failureCount, err) => {
      if (isNotConfigured(err) || isApiErrorStatus(err, 403)) return false;
      return failureCount < 2;
    },
  });
}

/**
 * Read a single transcript's recorded git commits
 * (`GET /api/v1/transcripts/{id}/commits`). Used by the commit-timeline overlay
 * to build a `sha → transcripts[]` map: each collective transcript's commit
 * SHAs are joined against a linked repo's cached commits to surface which
 * commits a transcript touched.
 *
 * Reads follow the transcript's own visibility (membership/ownership), so a 403
 * is terminal — we don't spin on it. The commit list is git history, effectively
 * immutable per transcript, so a long `staleTime` keeps the fan-out of per-id
 * fetches cheap when the overlay re-renders.
 */
export function useTranscriptCommits(
  transcriptId: string,
  enabled = true
) {
  return useQuery({
    queryKey: ["transcript-commits", transcriptId],
    queryFn: () =>
      api<TranscriptCommitsResponse>(
        `/transcripts/${encodeURIComponent(transcriptId)}/commits`
      ),
    enabled: enabled && !!transcriptId,
    // Per-transcript git history doesn't change after ingest; cache aggressively
    // so the overlay's per-id fan-out doesn't refetch on every interaction.
    staleTime: 5 * 60 * 1000,
    retry: (failureCount, err) => {
      if (isNotConfigured(err) || isApiErrorStatus(err, 403)) return false;
      return failureCount < 2;
    },
  });
}
