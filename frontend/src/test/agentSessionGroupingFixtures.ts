import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";
import { AGENT_ORIGIN, SESSION_ORIGINS, type SessionOrigin } from "@/lib/sessionOrigin";

export type AgentSessionGroupingSurface = "explore" | "profile";

/** How the server decided a case's session origin. `classified` is Village's
 *  own verdict over the content; `declared` is the producer's declaration,
 *  stored into the same column. The mounted surface must not tell them apart. */
export type AgentSessionOriginProvenance = "classified" | "declared";

const ORIGIN_PROVENANCES: readonly AgentSessionOriginProvenance[] = ["classified", "declared"];

export type AgentSessionGroupingCase = {
  name: string;
  surface: AgentSessionGroupingSurface;
  listedSessions: string[];
  agentSessions: string[];
  agentTotal: number;
  expectedToggleLabel: string | null;
  originProvenance: AgentSessionOriginProvenance;
};

export type AgentSessionDetailCase = {
  name: string;
  sessionOrigin: SessionOrigin;
  expectChip: boolean;
};

export type AgentSessionGroupingFixtures = {
  cases: AgentSessionGroupingCase[];
  detailCases: AgentSessionDetailCase[];
};

/** Deletion guard: every named case must be present. Names rather than a row
 *  count, so adding a case is one edit to the fixture and a deleted case still
 *  fails loudly by name. The detail cases are guarded the same way, by covering
 *  every session-origin menu value exactly once. */
const requiredCaseNames = [
  "explore-collapses-agent-sessions-out-of-the-browse-list",
  "explore-without-agent-sessions-renders-no-group",
  "profile-places-the-group-after-the-project-groups",
  "profile-library-made-only-of-agent-sessions-is-not-empty",
  "explore-groups-a-declared-agent-session",
] as const;

const caseKeys = [
  "name",
  "surface",
  "listedSessions",
  "agentSessions",
  "agentTotal",
  "expectedToggleLabel",
  "originProvenance",
];

export function loadAgentSessionGroupingFixtures(): AgentSessionGroupingFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/agent-session-grouping.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("agent-session-grouping fixture root must be an object");
  }
  assertExactKeys(parsed, ["cases", "detailCases"], "fixture root");
  const fixtures = parsed as AgentSessionGroupingFixtures;
  const names = fixtures.cases.map(({ name }) => name).sort();
  const wanted = [...requiredCaseNames].sort();
  if (JSON.stringify(names) !== JSON.stringify(wanted)) {
    throw new Error(`agent-session-grouping case names differ: got ${names.join(", ")}; want ${wanted.join(", ")}`);
  }
  const surfaces = new Set<string>();
  const provenances = new Set<string>();
  for (const c of fixtures.cases) {
    assertExactKeys(c, caseKeys, `case ${c.name}`);
    if (c.surface !== "explore" && c.surface !== "profile") {
      throw new Error(`case ${c.name}: surface must be explore or profile, got ${c.surface}`);
    }
    surfaces.add(c.surface);
    if (!ORIGIN_PROVENANCES.includes(c.originProvenance)) {
      throw new Error(
        `case ${c.name}: originProvenance must be one of ${ORIGIN_PROVENANCES.join(", ")}, got ${c.originProvenance}`,
      );
    }
    provenances.add(c.originProvenance);
    if (c.agentTotal !== c.agentSessions.length) {
      throw new Error(
        `case ${c.name}: agentTotal ${c.agentTotal} disagrees with ${c.agentSessions.length} agent sessions — the ` +
          `collapsed count and the rows behind it must describe the same set`,
      );
    }
    if ((c.expectedToggleLabel == null) !== (c.agentTotal === 0)) {
      throw new Error(
        `case ${c.name}: a case with agent sessions must expect a group label, and a case without them must expect none`,
      );
    }
    if (c.expectedToggleLabel != null && !c.expectedToggleLabel.includes(String(c.agentTotal))) {
      throw new Error(`case ${c.name}: the expected label ${c.expectedToggleLabel} does not name the count ${c.agentTotal}`);
    }
    // Within one case the two sets must be disjoint and internally unique:
    // the whole assertion is telling the listed rows and the grouped rows
    // apart in the DOM, which a shared id would make impossible.
    const caseIds = [...c.listedSessions, ...c.agentSessions];
    if (new Set(caseIds).size !== caseIds.length) {
      throw new Error(
        `case ${c.name}: a session id appears twice; the listed rows and the grouped rows must name different sessions`,
      );
    }
  }
  for (const surface of ["explore", "profile"]) {
    if (!surfaces.has(surface)) {
      throw new Error(`agent-session-grouping fixtures cover no ${surface} case`);
    }
  }
  for (const provenance of ORIGIN_PROVENANCES) {
    if (!provenances.has(provenance)) {
      throw new Error(
        `agent-session-grouping fixtures cover no ${provenance} case: a declared origin and a classified one are ` +
          `written into the same column, so the mounted surface must be proven to group and label both the same way`,
      );
    }
  }
  const detailOrigins = new Set<string>();
  for (const c of fixtures.detailCases) {
    assertExactKeys(c, ["name", "sessionOrigin", "expectChip"], `detail case ${c.name}`);
    if (!SESSION_ORIGINS.includes(c.sessionOrigin)) {
      throw new Error(
        `detail case ${c.name}: sessionOrigin ${c.sessionOrigin} is not in the session-origin menu ` +
          `(${SESSION_ORIGINS.join(", ")}); fix the value in src/testdata/agent-session-grouping.yaml`,
      );
    }
    if (detailOrigins.has(c.sessionOrigin)) {
      throw new Error(`detail case ${c.name}: origin ${c.sessionOrigin} is covered twice`);
    }
    detailOrigins.add(c.sessionOrigin);
    if (c.expectChip !== (c.sessionOrigin === AGENT_ORIGIN)) {
      throw new Error(
        `detail case ${c.name}: only an agent-driven session is labelled; unclassified sessions are presented like ` +
          `user sessions, so expectChip must be true for agent and false otherwise`,
      );
    }
  }
  for (const origin of SESSION_ORIGINS) {
    if (!detailOrigins.has(origin)) {
      throw new Error(`agent-session-grouping detail cases cover no ${origin} session`);
    }
  }
  return fixtures;
}
