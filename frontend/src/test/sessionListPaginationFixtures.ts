import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

/**
 * Typed loader for the mounted `/` session-pagination fixtures.
 *
 * The YAML holds every permutation (deferred pages, out-of-order/abort-ignored
 * schedules, settled mismatch, navigation after a rejected mismatch, terminal
 * failure and exact-key retry, filter reset, and accessibility boundaries).
 * This loader is the sole parser: it enforces an exact scenario inventory,
 * rejects unknown fields, and guarantees
 * unique names and referenced data ids so a scenario can never silently drop a
 * case or point at a missing response. The mounted test stays declarative and
 * drives the real route from the parsed structure.
 */

/** A discovery list response the schedule can hand back for a request. */
export type PaginationData = { page: number; ids: string[] };

/** The discriminated action a step performs against the mounted route. */
export type PaginationAction =
  | { kind: "resolve"; requestPage: number; dataId: string }
  | { kind: "resolveIgnoringAbort"; requestPage: number; dataId: string }
  | { kind: "reject"; requestPage: number }
  | { kind: "clickPage"; page: number }
  | { kind: "clickNext" }
  | { kind: "clickPrev" }
  | { kind: "clickRetry" }
  | { kind: "observe" }
  | { kind: "changeOrder"; order: string };

/** Observable expectations asserted after a step settles. */
export type PaginationExpect = {
  currentPage?: number | null;
  visibleIds?: string[];
  ariaBusy?: boolean;
  status?: string;
  statusIncludes?: string;
  /** A substring the visible loading cue must contain, or `false` to require its absence. */
  visibleLoading?: string | false;
  alert?: boolean;
  alertIncludes?: string[];
  requestedPages?: number[];
  abortedPages?: number[];
  focusPage?: number;
  uniqueIds?: boolean;
  prevDisabled?: boolean;
  nextDisabled?: boolean;
  singleCurrent?: boolean;
};

export type PaginationStep = { name: string; action: PaginationAction; expect: PaginationExpect };
export type PaginationScenario = { name: string; steps: PaginationStep[] };
export type SessionListPaginationFixtures = {
  data: Record<string, PaginationData>;
  scenarios: PaginationScenario[];
};

const requiredScenarioNames = [
  "deferred-second-page-keeps-intent-and-retains-rows",
  "later-page-supersedes-abort-ignoring-earlier",
  "settled-page-mismatch-keeps-prior-rows",
  "rejected-mismatch-then-new-page-keeps-last-confirmed-rows",
  "terminal-failure-then-exact-key-retry",
  "filter-change-returns-to-first-page",
  "keyboard-boundaries-and-single-current",
] as const;

const actionKinds = [
  "resolve",
  "resolveIgnoringAbort",
  "reject",
  "clickPage",
  "clickNext",
  "clickPrev",
  "clickRetry",
  "observe",
  "changeOrder",
] as const;

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === "object" && !Array.isArray(value);
}

function assertKeys(value: Record<string, unknown>, required: string[], optional: string[], location: string): void {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw new Error(`${location} has unknown field "${key}"; allowed: ${[...allowed].sort().join(", ")}`);
    }
  }
  for (const key of required) {
    if (!(key in value)) throw new Error(`${location} is missing required field "${key}"`);
  }
}

function assertBoolean(value: unknown, location: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${location} must be a boolean, got ${JSON.stringify(value)}`);
  return value;
}

function assertInteger(value: unknown, location: string): number {
  if (typeof value !== "number" || !Number.isInteger(value)) {
    throw new Error(`${location} must be an integer, got ${JSON.stringify(value)}`);
  }
  return value;
}

function assertString(value: unknown, location: string): string {
  if (typeof value !== "string") throw new Error(`${location} must be a string, got ${JSON.stringify(value)}`);
  return value;
}

function assertStringArray(value: unknown, location: string): string[] {
  if (!Array.isArray(value)) throw new Error(`${location} must be an array, got ${JSON.stringify(value)}`);
  return value.map((entry, index) => assertString(entry, `${location}[${index}]`));
}

function assertIntegerArray(value: unknown, location: string): number[] {
  if (!Array.isArray(value)) throw new Error(`${location} must be an array, got ${JSON.stringify(value)}`);
  return value.map((entry, index) => assertInteger(entry, `${location}[${index}]`));
}

function assertUniqueNames(names: string[], location: string): void {
  if (new Set(names).size !== names.length) throw new Error(`${location} names must be unique: ${names.join(", ")}`);
}

function parseAction(raw: unknown, data: Record<string, PaginationData>, location: string): PaginationAction {
  if (!isPlainObject(raw)) throw new Error(`${location} must be an object`);
  const kind = assertString(raw.kind, `${location}.kind`);
  if (!(actionKinds as readonly string[]).includes(kind)) {
    throw new Error(`${location}.kind must be one of ${actionKinds.join(", ")}, got ${JSON.stringify(kind)}`);
  }
  const requireData = (dataId: unknown): string => {
    const id = assertString(dataId, `${location}.dataId`);
    if (!(id in data)) throw new Error(`${location}.dataId references unknown data id ${JSON.stringify(id)}`);
    return id;
  };
  switch (kind) {
    case "resolve":
    case "resolveIgnoringAbort":
      assertKeys(raw, ["kind", "requestPage", "dataId"], [], location);
      return { kind, requestPage: assertInteger(raw.requestPage, `${location}.requestPage`), dataId: requireData(raw.dataId) };
    case "reject":
      assertKeys(raw, ["kind", "requestPage"], [], location);
      return { kind, requestPage: assertInteger(raw.requestPage, `${location}.requestPage`) };
    case "clickPage":
      assertKeys(raw, ["kind", "page"], [], location);
      return { kind, page: assertInteger(raw.page, `${location}.page`) };
    case "changeOrder":
      assertKeys(raw, ["kind", "order"], [], location);
      return { kind, order: assertString(raw.order, `${location}.order`) };
    default:
      assertKeys(raw, ["kind"], [], location);
      return { kind } as PaginationAction;
  }
}

function parseExpect(raw: unknown, location: string): PaginationExpect {
  if (!isPlainObject(raw)) throw new Error(`${location} must be an object`);
  assertKeys(
    raw,
    [],
    [
      "currentPage",
      "visibleIds",
      "ariaBusy",
      "status",
      "statusIncludes",
      "visibleLoading",
      "alert",
      "alertIncludes",
      "requestedPages",
      "abortedPages",
      "focusPage",
      "uniqueIds",
      "prevDisabled",
      "nextDisabled",
      "singleCurrent",
    ],
    location,
  );
  if (Object.keys(raw).length === 0) throw new Error(`${location} must assert at least one observable`);
  const out: PaginationExpect = {};
  if ("currentPage" in raw) {
    out.currentPage = raw.currentPage === null ? null : assertInteger(raw.currentPage, `${location}.currentPage`);
  }
  if ("visibleIds" in raw) out.visibleIds = assertStringArray(raw.visibleIds, `${location}.visibleIds`);
  if ("ariaBusy" in raw) out.ariaBusy = assertBoolean(raw.ariaBusy, `${location}.ariaBusy`);
  if ("status" in raw) out.status = assertString(raw.status, `${location}.status`);
  if ("statusIncludes" in raw) out.statusIncludes = assertString(raw.statusIncludes, `${location}.statusIncludes`);
  if ("visibleLoading" in raw) {
    const value = raw.visibleLoading;
    if (value !== false && typeof value !== "string") {
      throw new Error(`${location}.visibleLoading must be a string or the literal false, got ${JSON.stringify(value)}`);
    }
    out.visibleLoading = value;
  }
  if ("alert" in raw) out.alert = assertBoolean(raw.alert, `${location}.alert`);
  if ("alertIncludes" in raw) out.alertIncludes = assertStringArray(raw.alertIncludes, `${location}.alertIncludes`);
  if ("requestedPages" in raw) out.requestedPages = assertIntegerArray(raw.requestedPages, `${location}.requestedPages`);
  if ("abortedPages" in raw) out.abortedPages = assertIntegerArray(raw.abortedPages, `${location}.abortedPages`);
  if ("focusPage" in raw) out.focusPage = assertInteger(raw.focusPage, `${location}.focusPage`);
  if ("uniqueIds" in raw) out.uniqueIds = assertBoolean(raw.uniqueIds, `${location}.uniqueIds`);
  if ("prevDisabled" in raw) out.prevDisabled = assertBoolean(raw.prevDisabled, `${location}.prevDisabled`);
  if ("nextDisabled" in raw) out.nextDisabled = assertBoolean(raw.nextDisabled, `${location}.nextDisabled`);
  if ("singleCurrent" in raw) out.singleCurrent = assertBoolean(raw.singleCurrent, `${location}.singleCurrent`);
  return out;
}

export function loadSessionListPaginationFixtures(): SessionListPaginationFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/session-list-pagination.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (!isPlainObject(parsed)) throw new Error("session-list pagination fixture root must be an object");
  assertKeys(parsed, ["data", "scenarios"], [], "fixture root");

  const rawData = parsed.data;
  if (!isPlainObject(rawData)) throw new Error("fixture data must be an object map");
  const data: Record<string, PaginationData> = {};
  const seenIds = new Set<string>();
  for (const [id, value] of Object.entries(rawData)) {
    if (!isPlainObject(value)) throw new Error(`data ${id} must be an object`);
    assertKeys(value, ["page", "ids"], [], `data ${id}`);
    const ids = assertStringArray(value.ids, `data ${id}.ids`);
    if (ids.length === 0) throw new Error(`data ${id}.ids must be non-empty`);
    if (new Set(ids).size !== ids.length) throw new Error(`data ${id}.ids must be unique: ${ids.join(", ")}`);
    for (const rowId of ids) {
      if (seenIds.has(rowId)) throw new Error(`transcript id ${rowId} appears in more than one data entry; per-page ids must be globally unique`);
      seenIds.add(rowId);
    }
    data[id] = { page: assertInteger(value.page, `data ${id}.page`), ids };
  }

  const rawScenarios = parsed.scenarios;
  if (!Array.isArray(rawScenarios)) throw new Error("fixture scenarios must be an array");
  const scenarios: PaginationScenario[] = rawScenarios.map((rawScenario, index) => {
    if (!isPlainObject(rawScenario)) throw new Error(`scenario[${index}] must be an object`);
    assertKeys(rawScenario, ["name", "steps"], [], `scenario[${index}]`);
    const name = assertString(rawScenario.name, `scenario[${index}].name`);
    if (!Array.isArray(rawScenario.steps) || rawScenario.steps.length === 0) {
      throw new Error(`scenario ${name} must have a non-empty steps array`);
    }
    const steps: PaginationStep[] = rawScenario.steps.map((rawStep, stepIndex) => {
      const location = `scenario ${name} step[${stepIndex}]`;
      if (!isPlainObject(rawStep)) throw new Error(`${location} must be an object`);
      assertKeys(rawStep, ["name", "action", "expect"], [], location);
      return {
        name: assertString(rawStep.name, `${location}.name`),
        action: parseAction(rawStep.action, data, `${location}.action`),
        expect: parseExpect(rawStep.expect, `${location}.expect`),
      };
    });
    assertUniqueNames(steps.map((step) => step.name), `scenario ${name} step`);
    return { name, steps };
  });

  assertUniqueNames(scenarios.map((scenario) => scenario.name), "scenario");
  const got = [...scenarios.map((scenario) => scenario.name)].sort();
  const want = [...requiredScenarioNames].sort();
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`scenario inventory differs: got ${got.join(", ")}; want ${want.join(", ")}`);
  }

  return { data, scenarios };
}
