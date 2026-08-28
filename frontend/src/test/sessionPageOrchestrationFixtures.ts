import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

/** A discovery list response shape referenced by id from a step. */
export type OrchestrationData = { page: number; total: number; limit: number };

/** The mocked useTranscripts return status for one step. */
export type OrchestrationQuery = {
  dataId: string | null;
  isLoading: boolean;
  isFetching: boolean;
  isPlaceholderData: boolean;
  isError: boolean;
};

export type OrchestrationRenders = "explore" | "errorSurface" | "skeleton";

export type OrchestrationExpect = {
  renders: OrchestrationRenders;
  alert: boolean;
  displayedPage?: number;
  ariaBusy?: boolean;
  status?: string;
  refetchCalled?: boolean;
  visibleLoading?: boolean;
  /** The retry control's exact accessible name at this step. */
  retryLabel?: string;
  /** Whether the retry reports itself busy and refuses further presses. */
  retryBusy?: boolean;
};

export type OrchestrationStep = {
  name: string;
  query: OrchestrationQuery;
  expect: OrchestrationExpect;
  setFiltersPage?: number;
  /**
   * Change the SEARCH TEXT while keeping the page, so the request key changes
   * but the failure message does not. That pair is what tells a remembered
   * failure keyed on its request from one keyed on its text.
   */
  setFiltersQuery?: string;
  action?: "clickRetry";
};

export type OrchestrationScenario = { name: string; steps: OrchestrationStep[] };

export type SessionPageOrchestrationFixtures = {
  data: Record<string, OrchestrationData>;
  scenarios: OrchestrationScenario[];
};

const requiredScenarioNames = [
  "page-transition-error-and-exact-key-retry",
  "terminal-error-keeps-prior-rows-and-clears-busy",
  "abort-supersession-is-silent",
  "trust-boundary-error-without-prior-data-shows-error",
  "persistent-live-status-across-branches",
  "initial-load-error-shows-error-surface",
  "a-new-key-does-not-inherit-the-previous-key-failure",
] as const;

const rendersValues: readonly OrchestrationRenders[] = ["explore", "errorSurface", "skeleton"];

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === "object" && !Array.isArray(value);
}

function assertKeys(
  value: Record<string, unknown>,
  required: string[],
  optional: string[],
  location: string,
): void {
  const allowed = new Set([...required, ...optional]);
  const actual = Object.keys(value);
  for (const key of actual) {
    if (!allowed.has(key)) {
      throw new Error(`${location} has unknown field "${key}"; allowed: ${[...allowed].sort().join(", ")}`);
    }
  }
  for (const key of required) {
    if (!(key in value)) {
      throw new Error(`${location} is missing required field "${key}"`);
    }
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

function assertUniqueNames(names: string[], location: string): void {
  if (new Set(names).size !== names.length) {
    throw new Error(`${location} names must be unique: ${names.join(", ")}`);
  }
}

export function loadSessionPageOrchestrationFixtures(): SessionPageOrchestrationFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/session-page-orchestration.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (!isPlainObject(parsed)) throw new Error("session page orchestration fixture root must be an object");
  assertKeys(parsed, ["data", "scenarios"], [], "fixture root");

  const rawData = parsed.data;
  if (!isPlainObject(rawData)) throw new Error("fixture data must be an object map");
  const data: Record<string, OrchestrationData> = {};
  for (const [id, value] of Object.entries(rawData)) {
    if (!isPlainObject(value)) throw new Error(`data ${id} must be an object`);
    assertKeys(value, ["page", "total", "limit"], [], `data ${id}`);
    data[id] = {
      page: assertInteger(value.page, `data ${id}.page`),
      total: assertInteger(value.total, `data ${id}.total`),
      limit: assertInteger(value.limit, `data ${id}.limit`),
    };
  }

  const rawScenarios = parsed.scenarios;
  if (!Array.isArray(rawScenarios)) throw new Error("fixture scenarios must be an array");
  const scenarios: OrchestrationScenario[] = rawScenarios.map((rawScenario, index) => {
    if (!isPlainObject(rawScenario)) throw new Error(`scenario[${index}] must be an object`);
    assertKeys(rawScenario, ["name", "steps"], [], `scenario[${index}]`);
    const name = assertString(rawScenario.name, `scenario[${index}].name`);
    if (!Array.isArray(rawScenario.steps) || rawScenario.steps.length === 0) {
      throw new Error(`scenario ${name} must have a non-empty steps array`);
    }
    const steps: OrchestrationStep[] = rawScenario.steps.map((rawStep, stepIndex) => {
      const location = `scenario ${name} step[${stepIndex}]`;
      if (!isPlainObject(rawStep)) throw new Error(`${location} must be an object`);
      assertKeys(rawStep, ["name", "query", "expect"], ["setFiltersPage", "setFiltersQuery", "action"], location);
      const stepName = assertString(rawStep.name, `${location}.name`);

      const rawQuery = rawStep.query;
      if (!isPlainObject(rawQuery)) throw new Error(`${location}.query must be an object`);
      assertKeys(rawQuery, ["dataId", "isLoading", "isFetching", "isPlaceholderData", "isError"], [], `${location}.query`);
      const dataId = rawQuery.dataId;
      if (dataId !== null && (typeof dataId !== "string" || !(dataId in data))) {
        throw new Error(`${location}.query.dataId must be null or a known data id, got ${JSON.stringify(dataId)}`);
      }
      const query: OrchestrationQuery = {
        dataId: dataId as string | null,
        isLoading: assertBoolean(rawQuery.isLoading, `${location}.query.isLoading`),
        isFetching: assertBoolean(rawQuery.isFetching, `${location}.query.isFetching`),
        isPlaceholderData: assertBoolean(rawQuery.isPlaceholderData, `${location}.query.isPlaceholderData`),
        isError: assertBoolean(rawQuery.isError, `${location}.query.isError`),
      };

      const rawExpect = rawStep.expect;
      if (!isPlainObject(rawExpect)) throw new Error(`${location}.expect must be an object`);
      assertKeys(
        rawExpect,
        ["renders", "alert"],
        ["displayedPage", "ariaBusy", "status", "refetchCalled", "visibleLoading", "retryLabel", "retryBusy"],
        `${location}.expect`,
      );
      const renders = assertString(rawExpect.renders, `${location}.expect.renders`) as OrchestrationRenders;
      if (!rendersValues.includes(renders)) {
        throw new Error(`${location}.expect.renders must be one of ${rendersValues.join(", ")}`);
      }
      const expectation: OrchestrationExpect = {
        renders,
        alert: assertBoolean(rawExpect.alert, `${location}.expect.alert`),
      };
      if ("displayedPage" in rawExpect) {
        expectation.displayedPage = assertInteger(rawExpect.displayedPage, `${location}.expect.displayedPage`);
      }
      if ("ariaBusy" in rawExpect) {
        expectation.ariaBusy = assertBoolean(rawExpect.ariaBusy, `${location}.expect.ariaBusy`);
      }
      if ("status" in rawExpect) {
        expectation.status = assertString(rawExpect.status, `${location}.expect.status`);
      }
      if ("refetchCalled" in rawExpect) {
        expectation.refetchCalled = assertBoolean(rawExpect.refetchCalled, `${location}.expect.refetchCalled`);
      }
      if ("retryLabel" in rawExpect) {
        const raw = rawExpect.retryLabel;
        if (typeof raw !== "string") {
          throw new Error(`${location}.expect.retryLabel must be a string`);
        }
        expectation.retryLabel = raw;
      }
      if ("retryBusy" in rawExpect) {
        expectation.retryBusy = assertBoolean(rawExpect.retryBusy, `${location}.expect.retryBusy`);
      }
      if ("visibleLoading" in rawExpect) {
        expectation.visibleLoading = assertBoolean(rawExpect.visibleLoading, `${location}.expect.visibleLoading`);
      }
      // The visible loading cue lives only in the results branch, so a scenario
      // may only assert it there.
      if (expectation.visibleLoading === true && renders !== "explore") {
        throw new Error(`${location}.expect may only set visibleLoading true when renders is "explore"`);
      }
      // The `explore` branch is the only one that renders a page; require the
      // displayedPage assertion exactly there so a scenario cannot silently skip
      // the row-identity check that proves which page is shown.
      if (renders === "explore" && expectation.displayedPage == null) {
        throw new Error(`${location}.expect must set displayedPage when renders is "explore"`);
      }
      if (renders !== "explore" && expectation.displayedPage != null) {
        throw new Error(`${location}.expect must not set displayedPage unless renders is "explore"`);
      }

      const step: OrchestrationStep = { name: stepName, query, expect: expectation };
      if ("setFiltersPage" in rawStep && "setFiltersQuery" in rawStep) {
        throw new Error(
          `${location}: a step changes either the page or the search text, not both; changing ` +
            `both cannot tell a request-keyed memory from a text-keyed one`,
        );
      }
      if ("setFiltersQuery" in rawStep) {
        const raw = rawStep.setFiltersQuery;
        if (typeof raw !== "string") {
          throw new Error(`${location}.setFiltersQuery must be a string`);
        }
        step.setFiltersQuery = raw;
      }
      if ("setFiltersPage" in rawStep) {
        step.setFiltersPage = assertInteger(rawStep.setFiltersPage, `${location}.setFiltersPage`);
      }
      if ("action" in rawStep) {
        const action = assertString(rawStep.action, `${location}.action`);
        if (action !== "clickRetry") throw new Error(`${location}.action must be "clickRetry"`);
        step.action = action;
      }
      return step;
    });
    assertUniqueNames(steps.map((step) => step.name), `scenario ${name} step`);
    return { name, steps };
  });

  assertUniqueNames(scenarios.map((scenario) => scenario.name), "scenario");
  const got = [...scenarios.map((s) => s.name)].sort();
  const want = [...requiredScenarioNames].sort();
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`scenario inventory differs: got ${got.join(", ")}; want ${want.join(", ")}`);
  }

  return { data, scenarios };
}
