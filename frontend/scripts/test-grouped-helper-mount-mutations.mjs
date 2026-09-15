import { readFile, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { parse } from "yaml";

/**
 * Proves the mounted helper-group cases CONSUME their fixture fields.
 *
 * Each mutation edits one anchor in a corpus and requires the named
 * production-path test to fail: a changed expectation must fail an assertion,
 * and a damaged corpus (a renamed required case, a non-boolean field, a page
 * composition with no continuation, a renamed collective case) must fail
 * loader validation. A mutation
 * that survives means the case's declared field is inert — the review finding
 * this script exists to prevent from coming back.
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
    const result = spawnSync(
      "pnpm",
      ["exec", "vitest", "run", mutation.testFile, "-t", mutation.testName],
      { cwd: new URL("..", import.meta.url), encoding: "utf8" },
    );
    const output = `${result.stdout ?? ""}${result.stderr ?? ""}`;
    if (result.status === 0) {
      throw new Error(
        `${mutation.name} survived: the corpus edit did not change the mounted test, so its declared field is inert`,
      );
    }
    if (mutation.failure === "assertion" && !output.includes(mutation.testName)) {
      throw new Error(
        `${mutation.name} failed without running ${mutation.testName}; the mutated corpus did not reach that test`,
      );
    }
    if (mutation.failure === "loader" && !output.includes("grouped helper")) {
      throw new Error(
        `${mutation.name} failed without the fixture loader naming the corpus violation`,
      );
    }
    process.stdout.write(`mutation killed: ${mutation.name}\n`);
  } finally {
    await writeFile(sourceURL, source);
  }
}
