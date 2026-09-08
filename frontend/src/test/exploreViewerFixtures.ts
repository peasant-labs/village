import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { isHarness, type Harness } from "@peasant-labs/schema";
import type { TranscriptListResponse, User } from "@/lib/types";
import { makeTranscriptFixture } from "./transcriptRowFixture";

const REQUIRED_NAMES = ["credentialed-cold-pending", "credentialed-cold-401", "cold-network-retry-repeated-recovery", "cold-server-retry-repeated-recovery", "home-network-retry-repeated-recovery", "home-server-retry-repeated-recovery", "viewer-a-to-b", "viewer-a-to-anonymous", "viewer-b-to-a", "viewer-a-error-to-b", "viewer-a-error-to-anonymous", "same-viewer-page-failure-retry", "populated-auth-failure-recovery"];
REQUIRED_NAMES.push("credential-change-before-auth-success", "credential-change-before-auth-401");
export type Viewer = "viewer-a" | "viewer-b" | "anonymous";
type ViewerData = { row: string; title: string; harness: Harness; count: number };
type Schedule =
  | { name: string; kind: "credential-race"; status: 200 | 401 }
  | { name: string; kind: "cold"; target: Viewer; failure: "none" | "network" | "server"; route: "home" | "explore" }
  | { name: string; kind: "transition"; initial: Viewer; target: Viewer; prior_error: boolean }
  | { name: string; kind: "retention"; initial: Viewer }
  | { name: string; kind: "auth-failure"; initial: Viewer };
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("viewer fixture requires object");
  return value as Record<string, unknown>;
}
function keys(value: Record<string, unknown>, expected: string) {
  if (Object.keys(value).sort().join(",") !== expected) throw new Error("unexpected viewer fixture fields");
}
function viewer(value: unknown): value is Viewer {
  return value === "viewer-a" || value === "viewer-b" || value === "anonymous";
}
export function validateExploreViewerFixtures(input: unknown) {
  const root = object(input);
  keys(root, "cases,viewers");
  const viewers = object(root.viewers);
  keys(viewers, "anonymous,viewer-a,viewer-b");
  const rowNames = new Set<string>();
  const facetIdentities = new Set<string>();
  for (const value of Object.values(viewers)) {
    const entry = object(value);
    keys(entry, "count,harness,row,title");
    if (typeof entry.row !== "string" || !entry.row.trim() || typeof entry.title !== "string" || !entry.title.trim()) throw new Error("viewer rows must be populated");
    if (!isHarness(entry.harness) || typeof entry.count !== "number" || !Number.isSafeInteger(entry.count) || entry.count <= 1) throw new Error("viewer facets must be populated independently of page rows");
    if (rowNames.has(entry.row)) throw new Error("viewer rows must be distinctive");
    rowNames.add(entry.row);
    const facetIdentity = `${entry.harness}:${entry.count}`;
    if (facetIdentities.has(facetIdentity)) throw new Error("viewer facets must be distinctive");
    facetIdentities.add(facetIdentity);
  }
  if (!Array.isArray(root.cases) || !root.cases.length) throw new Error("viewer schedules must be nonempty");
  const names = new Set<string>();
  const cases = root.cases.map((value) => {
    const entry = object(value);
    if (typeof entry.name !== "string" || !entry.name.trim()) throw new Error("viewer schedule name must be nonempty string");
    if (names.has(entry.name)) throw new Error("viewer schedule names must be unique");
    names.add(entry.name);
    switch (entry.kind) {
      case "credential-race":
        keys(entry, "kind,name,status");
        if (entry.status !== 200 && entry.status !== 401) throw new Error("invalid credential race status");
        break;
      case "cold":
        keys(entry, "failure,kind,name,route,target");
        if (!["none", "network", "server"].includes(entry.failure as string) || !["home", "explore"].includes(entry.route as string) || !viewer(entry.target)) throw new Error("invalid cold schedule");
        break;
      case "transition":
        keys(entry, "initial,kind,name,prior_error,target");
        if (!viewer(entry.initial) || !viewer(entry.target) || entry.initial === entry.target || typeof entry.prior_error !== "boolean") throw new Error("invalid viewer transition");
        break;
      case "retention": case "auth-failure":
        keys(entry, "initial,kind,name");
        if (!viewer(entry.initial)) throw new Error("invalid initial viewer");
        break;
      default: throw new Error("unsupported viewer schedule kind");
    }
    return entry as Schedule;
  });
  for (const name of REQUIRED_NAMES) if (!names.has(name)) throw new Error(`viewer fixture missing required case ${name}`);
  return { viewers: viewers as Record<Viewer, ViewerData>, cases };
}
export function loadExploreViewerFixtures() {
  return validateExploreViewerFixtures(parse(readFileSync(resolve(process.cwd(), "src/testdata/explore-viewer-schedules.yaml"), "utf8")));
}
export function fixtureUser(id: string): User {
  return { id, github_id: 1, github_username: id, display_name: null, avatar_url: null, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", is_discoverable: true, username_chosen: true, provider_username: null };
}
export function viewerResponse(data: ViewerData, id: Viewer, page = 1): TranscriptListResponse {
  return { transcripts: [{ transcript: makeTranscriptFixture({ id: data.row, title: data.title, owner_id: id, model_provider: data.harness }), owner: fixtureUser(id), tags: [] }], harness_facets: [{ harness: data.harness, count: data.count }], total: 48, agent_total: 0, page, limit: 24 };
}
