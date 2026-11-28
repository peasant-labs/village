/* Mutation runner for the transcript page's positioned reader.

   Applies each source mutation in testdata/positioned-host-mutations.yaml on its own, runs the
   mutation's named killer cases, and requires a case carrying the killer's name to be among the
   cases that FAILED. A run that fails without that case failing (a file that no longer compiles, a
   killer whose name matched nothing) is not a kill and stops the runner. The file is restored after
   every mutation, also when the runner throws.

   Journey killers need the journey mock and a dev server, so they run only with
   POSITIONED_MUTATIONS_JOURNEYS=1; the row's vitest killer always runs.

   usage: node scripts/test-positioned-host-mutations.mjs */
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { parse } from "yaml";

const WHERE = "test-positioned-host-mutations.mjs";
/** The test outcomes the vitest and Playwright JSON reports name, as far as this runner reads them. */
const ReportStatus = Object.freeze({ Failed: "failed", TimedOut: "timedOut" });
const fixtureURL = new URL("./testdata/positioned-host-mutations.yaml", import.meta.url);
const frontend = new URL("..", import.meta.url);
const withJourneys = process.env.POSITIONED_MUTATIONS_JOURNEYS === "1";

const root = parse(await readFile(fixtureURL, "utf8"), { strict: true });
if (root == null || typeof root !== "object" || Array.isArray(root) || Object.keys(root).join(",") !== "mutations") {
  throw new Error(`${WHERE}: the fixture root must contain only mutations; fix ${fixtureURL.pathname}.`);
}
const requiredNames = [
  "v2-error-reads-content",
  "version-token-substring",
  "version-current-member-only",
  "decide-on-min-alone",
  "decide-on-health-trait",
  "export-from-loaded-segments",
  "json-indent-dropped",
  "json-trailing-newline-dropped",
  "epoch-ignored",
  "revision-echo-dropped",
  "representation-echo-dropped",
  "http-date-read-as-seconds",
  "graph-reads-content-on-mount",
  "list-reads-content",
  "snapped-line-prints-recorded-index",
  "discovery-timeout-removed",
  "download-epoch-never-ends",
  "saved-turn-by-position",
  "saved-turn-not-targeted",
  "saved-history-not-reopened",
  "stopped-download-silent",
  "restored-offset-dropped",
  "scroll-save-unthrottled",
  "scroll-save-writes-unchanged",
  "restore-offset-ignores-user-scroll",
  "restore-offset-ignores-out-of-view",
  "absent-recorded-index-clears-saved",
];
const names = root.mutations.map(({ name }) => name);
if (new Set(names).size !== names.length || JSON.stringify([...names].sort()) !== JSON.stringify([...requiredNames].sort())) {
  throw new Error(`${WHERE}: mutation names differ from the required list; got ${names.join(", ")}.`);
}

const scratch = mkdtempSync(join(tmpdir(), "positioned-mutations-"));
/** The file under mutation and its original text, restored on any exit, a signal or a closed pipe. */
let pending = null;
const restore = () => {
  if (pending) writeFileSync(pending.url, pending.source);
  pending = null;
};
process.on("exit", () => {
  restore();
  rmSync(scratch, { recursive: true, force: true });
});
for (const signal of ["SIGINT", "SIGTERM", "SIGHUP", "SIGPIPE"]) process.on(signal, () => process.exit(1));
process.stdout.on("error", () => process.exit(1));

/** The assertion entries a killer run reported (exact leaf title, ancestor suite titles, and status). */
function runKiller(killer) {
  const report = join(scratch, "report.json");
  rmSync(report, { force: true });
  if (killer.kind === "vitest") {
    spawnSync("pnpm", ["exec", "vitest", "run", killer.file, "-t", killer.name, "--reporter=json", `--outputFile=${report}`], {
      cwd: frontend, encoding: "utf8", env: { ...process.env, NODE_OPTIONS: "--no-experimental-webstorage" },
    });
    const result = JSON.parse(readFileSync(report, "utf8"));
    return result.testResults.flatMap((file) => file.assertionResults).map((test) => ({
      title: test.title,
      ancestorTitles: test.ancestorTitles ?? [],
      failed: test.status === ReportStatus.Failed,
    }));
  }
  if (killer.kind === "journey") {
    const run = spawnSync("pnpm", ["exec", "playwright", "test", "--config", "playwright.journey.config.mjs", killer.file, "-g", killer.name, "--project", "dark", "--reporter=json"], {
      cwd: frontend, encoding: "utf8", env: { ...process.env, JOURNEY_HTML: "0" }, maxBuffer: 64 * 1024 * 1024,
    });
    const result = JSON.parse(run.stdout);
    const entries = [];
    const walk = (suite, path) => {
      for (const spec of suite.specs ?? []) {
        entries.push({
          title: spec.title,
          ancestorTitles: path,
          failed: spec.tests.some((test) => test.results.some((r) => r.status === ReportStatus.Failed || r.status === ReportStatus.TimedOut)),
        });
      }
      for (const child of suite.suites ?? []) walk(child, [...path, child.title]);
    };
    for (const suite of result.suites ?? []) walk(suite, []);
    return entries;
  }
  throw new Error(`${WHERE}: killer kind ${JSON.stringify(killer.kind)} is not vitest or journey.`);
}

let killed = 0;
let skipped = 0;
for (const mutation of root.mutations) {
  const sourceURL = new URL(mutation.file, fixtureURL);
  const source = await readFile(sourceURL, "utf8");
  const expected = mutation.occurrences ?? 1;
  const found = source.split(mutation.original).length - 1;
  if (found !== expected) {
    throw new Error(`${WHERE}: ${mutation.name} expects its original text ${expected} time(s) in ${mutation.file}, found ${found}; update the fixture to the current source.`);
  }
  pending = { url: sourceURL, source };
  await writeFile(sourceURL, source.split(mutation.original).join(mutation.replacement));
  try {
    let ran = 0;
    for (const killer of mutation.killers) {
      if (killer.kind === "journey" && !withJourneys) {
        process.stdout.write(`skipped journey killer: ${mutation.name} -> ${killer.name} (set POSITIONED_MUTATIONS_JOURNEYS=1)\n`);
        continue;
      }
      ran += 1;
      const entries = runKiller(killer);
      const exactLeafMatches = entries.filter((entry) => entry.title === killer.name);
      if (exactLeafMatches.length > 1) {
        throw new Error(`${WHERE}: ${mutation.name} ${killer.kind} killer ${killer.name} matched ${exactLeafMatches.length} test titles; killer names must identify one test uniquely.`);
      }
      const failing = entries.filter((entry) => entry.failed);
      if (!failing.some((entry) => entry.title === killer.name || entry.ancestorTitles.includes(killer.name))) {
        throw new Error(
          `${WHERE}: ${mutation.name} survived ${killer.kind} killer ${killer.name}: ` +
            (failing.length ? `only ${failing.map((entry) => [...entry.ancestorTitles, entry.title].join(" ")).join("; ")} failed` : "no named case failed (did the file stop compiling, or does the killer name match nothing?)") +
            `. Restore the case or the production path it guards.`,
        );
      }
      process.stdout.write(`mutation killed: ${mutation.name} by ${killer.kind} ${killer.name}\n`);
    }
    if (ran > 0) {
      killed += 1;
    } else {
      skipped += 1;
      process.stdout.write(`SKIPPED (journey-only): ${mutation.name}\n`);
    }
  } finally {
    restore();
  }
}
process.stdout.write(`${killed} killed, ${skipped} skipped (of ${root.mutations.length} positioned-host mutations)\n`);
