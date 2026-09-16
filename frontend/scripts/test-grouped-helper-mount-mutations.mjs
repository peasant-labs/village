import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parse } from "yaml";
import { classifyFocusedOutcome } from "./lib/focusedMutationOutcome.mjs";

/**
 * Proves the mounted helper-group cases CONSUME their fixture fields.
 *
 * Each mutation edits one anchor in a corpus and declares how the edit must be
 * caught: `assertion` edits a declared expectation that must fail a mounted
 * assertion, and `loader` damages the corpus itself in a way the fixture loader
 * must refuse. The focused run is judged from its structured Vitest JSON report,
 * never from console text, because a broken corpus aborts the suite before any
 * test runs and its loader error still names the case. A mutation that survives
 * means the case's declared field is inert, and an `assertion` mutation the
 * loader caught first is reported as a BROKEN FIXTURE — a distinct failure class
 * that is never counted as a kill — because it proves nothing about the mounted
 * assertion it claims to reach.
 */

const fixtureURL = new URL("./testdata/grouped-helper-mount-mutations.yaml", import.meta.url);
const root = parse(await readFile(fixtureURL, "utf8"), { strict: true });
if (
  root == null ||
  typeof root !== "object" ||
  Array.isArray(root) ||
  Object.keys(root).join(",") !== "mutations"
) {
  throw new Error("grouped helper mount mutation fixture root must contain only mutations");
}
const requiredNames = [
  "expected-request-scope-drift",
  "expected-selected-drift",
  "expected-member-drift",
  "required-case-renamed",
  "invalid-expand-field",
  "continuation-total-drift",
  "collective-expected-id-drift",
  "collective-required-case-renamed",
  "collective-fallback-flat-mode-invalid",
  "collective-continuation-total-drift",
  "collective-continuation-case-renamed",
];
const names = root.mutations.map(({ name }) => name);
if (
  !Array.isArray(root.mutations) ||
  root.mutations.length !== requiredNames.length ||
  new Set(names).size !== names.length ||
  JSON.stringify([...names].sort()) !== JSON.stringify([...requiredNames].sort())
) {
  throw new Error("grouped helper mount mutation name inventory differs");
}

/** Run one mutation's focused test and return its parsed JSON report. */
async function focusedReport(mutation) {
  const scratch = await mkdtemp(join(tmpdir(), "grouped-helper-mutation-"));
  const reportPath = join(scratch, "vitest-report.json");
  try {
    const result = spawnSync(
      "pnpm",
      [
        "exec",
        "vitest",
        "run",
        mutation.testFile,
        "-t",
        mutation.testName,
        "--reporter=json",
        `--outputFile=${reportPath}`,
      ],
      { cwd: new URL("..", import.meta.url), encoding: "utf8" },
    );
    try {
      return JSON.parse(await readFile(reportPath, "utf8"));
    } catch {
      throw new Error(
        `${mutation.name} produced no readable Vitest JSON report (vitest exited ${result.status}), so the focused run could not be classified and nothing is claimed about this mutation`,
      );
    }
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}

let assertionKills = 0;
let loaderKills = 0;

for (const mutation of root.mutations) {
  const fields = ["file", "name", "original", "replacement", "testFile", "testName", "failure"];
  if (JSON.stringify(Object.keys(mutation).sort()) !== JSON.stringify(fields.sort())) {
    throw new Error(`${mutation.name} has unknown or missing fields`);
  }
  if (mutation.failure !== "assertion" && mutation.failure !== "loader") {
    throw new Error(`${mutation.name} states an unknown failure mode ${mutation.failure}`);
  }
  const sourceURL = new URL(mutation.file, fixtureURL);
  const source = await readFile(sourceURL, "utf8");
  if (source.split(mutation.original).length !== 2) {
    throw new Error(`${mutation.name} anchor must occur exactly once`);
  }
  await writeFile(sourceURL, source.replace(mutation.original, mutation.replacement));
  try {
    const outcome = classifyFocusedOutcome(await focusedReport(mutation), mutation.testName);
    if (outcome.kind === "passed") {
      throw new Error(
        `${mutation.name} survived: the corpus edit did not change the mounted test, so its declared field is inert`,
      );
    }
    if (mutation.failure === "assertion") {
      if (outcome.kind !== "assertion-failure") {
        throw new Error(
          `BROKEN FIXTURE ${mutation.name}: the corpus edit was caught at fixture load, not by the mounted assertion "${mutation.testName}", so the run proves nothing about that assertion. loader detail: ${outcome.detail}`,
        );
      }
      assertionKills += 1;
      process.stdout.write(`mutation killed at its mounted assertion: ${mutation.name}\n`);
    } else {
      if (outcome.kind !== "load-failure") {
        throw new Error(
          `MISDECLARED MUTATION ${mutation.name}: it declares a fixture-loader kill but the run failed at an executed assertion instead. assertion detail: ${outcome.detail}`,
        );
      }
      if (!outcome.detail.includes("grouped helper")) {
        throw new Error(
          `${mutation.name} failed at load without the fixture loader naming the corpus violation: ${outcome.detail}`,
        );
      }
      loaderKills += 1;
      process.stdout.write(`mutation killed at loader validation: ${mutation.name}\n`);
    }
  } finally {
    await writeFile(sourceURL, source);
  }
}

process.stdout.write(
  `all ${root.mutations.length} mutations killed: ${assertionKills} at their mounted assertion, ${loaderKills} at loader validation\n`,
);
