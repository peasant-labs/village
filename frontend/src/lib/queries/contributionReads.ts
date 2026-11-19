import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import {
  zVillageContributionPageResponse,
  zVillageContributionProjectPage,
  zVillageContributionSnapshotResponse,
  type VillageContributionPageResponse,
  type VillageContributionProjectPage,
  type VillageContributionSnapshotResponse,
} from "@peasant-labs/schema";
import { useAuth } from "@/providers/AuthProvider";
import { api, ApiError } from "../api";

/**
 * The contribute page's three reads of `GET /collectives/{id}/contributable`:
 * one page of families (`view=paged`), the project picker
 * (`/contributable/projects`), and the exact set of one project
 * (`view=snapshot`).
 *
 * A backend that predates these views treats any `view` other than `grouped` as
 * the flat read, so it answers a paged or snapshot request with a flat 200, or
 * with a 413 once the library passes the flat read's cap. It never answers 404
 * there. A status alone therefore proves nothing: every body is checked for the
 * variant that was asked for, and a body of another variant is a deployment
 * mismatch that shows no rows at all.
 */

/** Families per page, counted in ROOT sessions: a root's helpers ride with it. */
export const CONTRIBUTION_PAGE_ROOTS = 500;

/** Projects per picker page. */
export const CONTRIBUTION_PICKER_PROJECTS = 500;

/** The search and provider filters every read narrows by. */
export interface ContributionFilters {
  q?: string;
  provider?: string;
}

/** The paged read also narrows by the root's project. */
export interface ContributionPageFilters extends ContributionFilters {
  project_hash?: string;
}

/**
 * The server answered with a body that is not the variant the client asked
 * for, or a refusal only an older backend gives. Nothing from such a response
 * may reach the page: it states the deployment is incomplete instead.
 */
export class ContributionDeploymentError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ContributionDeploymentError";
  }
}

/** Which read failed. The same status means different things on each. */
export type ContributionRead = "page" | "projects" | "snapshot";

/**
 * Why a read failed, in the terms the page acts on.
 *
 * - `deployment`: the backend does not serve this read; no rows are shown.
 * - `too_large`: the snapshot is over the contract's size limit; narrow the
 *   search and try again. Nothing partial was returned.
 * - `busy`: the server could not take the snapshot now; retry.
 * - `cancelled`: the person cancelled the read.
 * - `failed`: any other failure; retry.
 */
export type ContributionReadFailure =
  | { kind: "deployment"; message: string }
  | { kind: "too_large" }
  | { kind: "busy" }
  | { kind: "cancelled" }
  | { kind: "failed"; message: string };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** A body that carries a legacy variant's list is never one of the new views,
 *  whatever else it carries. */
function legacyVariant(raw: Record<string, unknown>): string | null {
  if ("transcripts" in raw) return "the flat list";
  if ("transcriptList" in raw) return "the grouped list";
  return null;
}

function mismatch(read: string, detail: string): ContributionDeploymentError {
  return new ContributionDeploymentError(`the ${read} answered with ${detail}; the backend deployment is incomplete`);
}

function parseOrMismatch<T>(read: string, result: { success: true; data: T } | { success: false }): T {
  if (!result.success) throw mismatch(read, "a body outside the contract");
  return result.data;
}

/** Validates one paged-view body before it can become cache data. */
export function decodeContributionPage(raw: unknown, groupId: string): VillageContributionPageResponse {
  const read = "contribution page";
  if (!isRecord(raw)) throw mismatch(read, "a body that is not an object");
  const legacy = legacyVariant(raw);
  if (legacy) throw mismatch(read, legacy);
  if (!("items" in raw) || !("has_more" in raw)) throw mismatch(read, "a body without items and has_more");
  const page = parseOrMismatch(read, zVillageContributionPageResponse.safeParse(raw));
  if (page.group_id !== groupId) throw mismatch(read, "a different collective");
  if (page.has_more && !page.next_cursor) throw mismatch(read, "more pages but no cursor");
  return page;
}

/** Validates one picker body before it can become cache data. */
export function decodeContributionProjects(raw: unknown): VillageContributionProjectPage {
  const read = "project list";
  if (!isRecord(raw)) throw mismatch(read, "a body that is not an object");
  const legacy = legacyVariant(raw);
  if (legacy) throw mismatch(read, legacy);
  if (!("projects" in raw)) throw mismatch(read, "a body without projects");
  const page = parseOrMismatch(read, zVillageContributionProjectPage.safeParse(raw));
  if (page.has_more && !page.next_cursor) throw mismatch(read, "more pages but no cursor");
  return page;
}

/** Validates a snapshot body. Its `count` must equal its records: the client
 *  never trusts a count it cannot see. */
export function decodeContributionSnapshot(
  raw: unknown,
  groupId: string,
  projectHash: string,
): VillageContributionSnapshotResponse {
  const read = "project snapshot";
  if (!isRecord(raw)) throw mismatch(read, "a body that is not an object");
  const legacy = legacyVariant(raw);
  if (legacy) throw mismatch(read, legacy);
  if (!("records" in raw)) throw mismatch(read, "a body without records");
  const snapshot = parseOrMismatch(read, zVillageContributionSnapshotResponse.safeParse(raw));
  if (snapshot.count !== snapshot.records.length) throw mismatch(read, "a count that differs from its records");
  if (snapshot.group_id !== groupId) throw mismatch(read, "a different collective");
  if (snapshot.project_hash !== projectHash) throw mismatch(read, "a different project");
  return snapshot;
}

/** Turns a thrown read failure into what the page does about it. */
export function classifyContributionReadError(err: unknown, read: ContributionRead): ContributionReadFailure {
  if (err instanceof ContributionDeploymentError) return { kind: "deployment", message: err.message };
  if (err instanceof DOMException && err.name === "AbortError") return { kind: "cancelled" };
  if (err instanceof ApiError) {
    if (err.status === 413) {
      // The snapshot's own size limit is the only 413 the new reads give. Any
      // other 413 is the flat read's cap, which only an older backend serves.
      if (read === "snapshot" && err.code === "snapshot_too_large") return { kind: "too_large" };
      return { kind: "deployment", message: `the ${read} read was refused as too large without the snapshot limit code; the backend deployment is incomplete` };
    }
    if (read === "projects" && (err.status === 404 || err.status === 405)) {
      return { kind: "deployment", message: "the project list is not served; the backend deployment is incomplete" };
    }
    if (read === "snapshot" && err.status === 503 && (err.code === "snapshot_timeout" || err.code === "snapshot_busy")) {
      return { kind: "busy" };
    }
    return { kind: "failed", message: err.message };
  }
  return { kind: "failed", message: err instanceof Error ? err.message : "the read failed unexpectedly" };
}

function setFilters(query: URLSearchParams, filters: ContributionFilters): void {
  if (filters.q) query.set("q", filters.q);
  if (filters.provider) query.set("provider", filters.provider);
}

export function contributionPagePath(groupId: string, filters: ContributionPageFilters, cursor?: string): string {
  const query = new URLSearchParams({ view: "paged", limit: String(CONTRIBUTION_PAGE_ROOTS) });
  setFilters(query, filters);
  if (filters.project_hash) query.set("project_hash", filters.project_hash);
  if (cursor) query.set("cursor", cursor);
  return `/collectives/${encodeURIComponent(groupId)}/contributable?${query.toString()}`;
}

export function contributionProjectsPath(groupId: string, filters: ContributionFilters, cursor?: string): string {
  const query = new URLSearchParams({ limit: String(CONTRIBUTION_PICKER_PROJECTS) });
  setFilters(query, filters);
  if (cursor) query.set("cursor", cursor);
  return `/collectives/${encodeURIComponent(groupId)}/contributable/projects?${query.toString()}`;
}

export function contributionSnapshotPath(groupId: string, projectHash: string, filters: ContributionFilters): string {
  const query = new URLSearchParams({ view: "snapshot", project_hash: projectHash });
  setFilters(query, filters);
  return `/collectives/${encodeURIComponent(groupId)}/contributable?${query.toString()}`;
}

/** The query-key root every contribution read shares, so a submission's
 *  invalidation of `["group-contributable", groupId]` refreshes all of them. */
const KEY_ROOT = "group-contributable";

/**
 * The paged families, one page at a time. A later page resumes from the
 * server's opaque cursor, so a row added meanwhile can neither repeat a family
 * nor skip one.
 */
export function useContributionPages(groupId: string, filters: ContributionPageFilters) {
  const { user, isLoading } = useAuth();
  const client = useQueryClient();
  const queryKey = [KEY_ROOT, groupId, "paged", user?.id ?? "anonymous", filters] as const;
  const query = useInfiniteQuery({
    queryKey,
    queryFn: async ({ pageParam, signal }) =>
      decodeContributionPage(await api<unknown>(contributionPagePath(groupId, filters, pageParam), { signal }), groupId),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.has_more ? last.next_cursor : undefined),
    enabled: !isLoading && !!groupId,
    retry: false,
    staleTime: 0,
  });
  return {
    ...query,
    items: query.data?.pages.flatMap((page) => page.items) ?? [],
    failure: query.error ? classifyContributionReadError(query.error, "page") : null,
    refresh: () => client.invalidateQueries({ queryKey, exact: true }),
  };
}

/** The project picker: every project with matching families, with its counts
 *  and the provider facets, whether or not any of its rows are loaded. */
export function useContributionProjects(groupId: string, filters: ContributionFilters) {
  const { user, isLoading } = useAuth();
  const client = useQueryClient();
  const queryKey = [KEY_ROOT, groupId, "projects", user?.id ?? "anonymous", filters] as const;
  const query = useInfiniteQuery({
    queryKey,
    queryFn: async ({ pageParam, signal }) =>
      decodeContributionProjects(await api<unknown>(contributionProjectsPath(groupId, filters, pageParam), { signal })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.has_more ? last.next_cursor : undefined),
    enabled: !isLoading && !!groupId,
    retry: false,
    staleTime: 0,
  });
  const first = query.data?.pages[0];
  return {
    ...query,
    projects: query.data?.pages.flatMap((page) => page.projects) ?? [],
    // The totals and facets describe the whole filtered library, so they come
    // from the first page and do not grow as later pages load.
    matchingTotal: first?.matching_total ?? 0,
    selectableTotal: first?.selectable_total ?? 0,
    providers: first?.providers ?? [],
    failure: query.error ? classifyContributionReadError(query.error, "projects") : null,
    refresh: () => client.invalidateQueries({ queryKey, exact: true }),
  };
}

/**
 * Reads the exact set of one project under the given filters. It is an action,
 * not a cached query: the result is merged into the selection once and then
 * frozen, so a row that matches later never joins it.
 */
export async function fetchContributionSnapshot(
  groupId: string,
  projectHash: string,
  filters: ContributionFilters,
  signal?: AbortSignal,
): Promise<VillageContributionSnapshotResponse> {
  const raw = await api<unknown>(contributionSnapshotPath(groupId, projectHash, filters), { signal });
  return decodeContributionSnapshot(raw, groupId, projectHash);
}
