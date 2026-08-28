import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/home-page.yaml` — the case corpus behind
 * `src/homePage.test.tsx`.
 *
 * Deletion protection is a required-NAME manifest per group. A deleted case
 * fails the loader because its name goes missing from the declared set, not
 * because a tally shrinks: a count guard churns on every legitimate addition
 * and conflicts whenever two changes append at once.
 *
 * Every consistency rule below is derived from the FIXTURE's own data, never
 * from the page's constants. A rule that read a production constant would
 * move with the code it is supposed to hold still.
 */

export type HomeRouteSurface = "home" | "explore";

export type HomeRouteCase = {
  name: string;
  path: string;
  viewerUsername: string | null;
  expectSurface: HomeRouteSurface;
};

export type HomeTranscriptCase = {
  id: string;
  title: string;
  projectHash: string;
  projectDisplayName: string;
  publishedAt: string;
};

export type HomeProjectRowCase = {
  displayName: string;
  sessionCount: number;
  href: string;
};

export type HomeCase = {
  name: string;
  viewerUsername: string;
  transcripts: HomeTranscriptCase[];
  expectRecentTitles: string[];
  expectProjectRows: HomeProjectRowCase[];
  expectEmptyState: boolean;
};

/**
 * A case as tests consume it: the fixture's own fields plus what the loader
 * derives from them.
 *
 * `malformedCount` is DERIVED, never written in the fixture — a hand-written
 * integer beside the rows that already state the fact is a tally to keep in
 * sync on every edit. It is a separate type rather than an optional field on
 * {@link HomeCase} so the YAML shape stays honest: `HomeCase` describes what
 * the file may contain, and the strict-key check is written against exactly
 * that.
 */
export type LoadedHomeCase = HomeCase & {
  /** How many supplied rows carry no project identity, and so are reported as
   *  an anomaly rather than grouped. */
  malformedCount: number;
};

export type HomeNavCase = {
  name: string;
  isLoggedIn: boolean;
  pathname: string;
  expectLabels: string[];
  expectActiveLabel: string;
};

/** The parsed file, before the loader derives anything. */
type ParsedHomePageFixtures = {
  routeCases: HomeRouteCase[];
  navCases: HomeNavCase[];
  homeCases: HomeCase[];
};

export type HomePageFixtures = {
  routeCases: HomeRouteCase[];
  navCases: HomeNavCase[];
  homeCases: LoadedHomeCase[];
};

const requiredRouteCaseNames = [
  "signed-out-visitor-at-the-root-still-gets-explore",
  "signed-in-visitor-at-the-root-gets-their-own-home",
  "signed-in-visitor-at-explore-gets-explore",
  "signed-out-visitor-at-explore-gets-explore",
] as const;

const requiredNavCaseNames = [
  "signed-in-visitor-at-the-root-highlights-home",
  "signed-in-visitor-at-explore-highlights-explore",
  "signed-out-visitor-at-the-root-highlights-explore",
  "signed-out-visitor-has-no-home-entry-at-explore",
  "a-transcript-page-still-highlights-explore",
] as const;

const navCaseKeys = ["name", "isLoggedIn", "pathname", "expectLabels", "expectActiveLabel"];

const requiredHomeCaseNames = [
  "recent-sessions-lead-and-projects-follow",
  "more-sessions-than-the-recent-list-shows-are-capped",
  "a-person-with-nothing-published-gets-the-teaching-empty-state",
  "a-username-needing-escaping-still-links-to-its-project",
  "a-row-arriving-without-a-project-identity-is-reported-not-dropped",
] as const;

const routeCaseKeys = ["name", "path", "viewerUsername", "expectSurface"];
const transcriptKeys = ["id", "title", "projectHash", "projectDisplayName", "publishedAt"];
const projectRowKeys = ["displayName", "sessionCount", "href"];
const homeCaseKeys = [
  "name",
  "viewerUsername",
  "transcripts",
  "expectRecentTitles",
  "expectProjectRows",
  "expectEmptyState",
];

const surfaces: readonly HomeRouteSurface[] = ["home", "explore"];

export function loadHomePageFixtures(): HomePageFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/home-page.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("home-page fixture root must be an object");
  }
  assertExactKeys(parsed, ["routeCases", "navCases", "homeCases"], "fixture root");
  const fixtures = parsed as ParsedHomePageFixtures;

  assertNamesMatch(
    fixtures.routeCases.map((c) => c.name),
    requiredRouteCaseNames,
    "home-page routeCases",
  );
  for (const c of fixtures.routeCases) {
    assertExactKeys(c, routeCaseKeys, `route case ${c.name}`);
    if (!surfaces.includes(c.expectSurface)) {
      throw new Error(
        `route case ${c.name}: ${c.expectSurface} is not a surface. The closed set is ` +
          `${surfaces.join(", ")}.`,
      );
    }
    // The rule the routes exist to express: home is the answer at `/`, and only
    // for somebody who is signed in. A case claiming otherwise would invert the
    // boundary rather than test it.
    const wantHome = c.path === "/" && c.viewerUsername !== null;
    if ((c.expectSurface === "home") !== wantHome) {
      throw new Error(
        `route case ${c.name}: expectSurface is ${c.expectSurface} for path ${c.path} viewed by ` +
          `${c.viewerUsername ?? "an anonymous visitor"}. Home is served at "/" and only to a ` +
          `signed-in visitor; fix the expectation rather than the rule.`,
      );
    }
  }
  // Both answers at `/` must be present, or the corpus proves only one branch
  // and a page that ignored the session entirely would still pass.
  const rootCases = fixtures.routeCases.filter((c) => c.path === "/");
  if (!rootCases.some((c) => c.viewerUsername === null) || !rootCases.some((c) => c.viewerUsername !== null)) {
    throw new Error(
      `home-page routeCases: "/" must be exercised BOTH signed in and signed out. A corpus that ` +
        `visits it one way cannot tell a session-aware root route from one that always renders ` +
        `the same surface.`,
    );
  }

  assertNamesMatch(
    fixtures.navCases.map((c) => c.name),
    requiredNavCaseNames,
    "home-page navCases",
  );
  for (const c of fixtures.navCases) {
    assertExactKeys(c, navCaseKeys, `nav case ${c.name}`);
    if (!c.expectLabels.includes(c.expectActiveLabel)) {
      throw new Error(
        `nav case ${c.name}: the active entry ${c.expectActiveLabel} is not among the entries the ` +
          `case expects (${c.expectLabels.join(", ")})`,
      );
    }
    // Home belongs to somebody who is signed in. A signed-out case listing it
    // would assert the opposite of the rule the entry exists to express.
    if (!c.isLoggedIn && c.expectLabels.includes("home")) {
      throw new Error(
        `nav case ${c.name}: a signed-out visitor has no home of their own, so the nav cannot ` +
          `offer a home entry`,
      );
    }
  }
  // Exactly one entry may be active, and both answers at "/" must be present,
  // or the corpus could not tell a session-aware nav from a fixed one.
  const rootNavCases = fixtures.navCases.filter((c) => c.pathname === "/");
  if (!rootNavCases.some((c) => c.isLoggedIn) || !rootNavCases.some((c) => !c.isLoggedIn)) {
    throw new Error(
      `home-page navCases: "/" must be exercised BOTH signed in and signed out`,
    );
  }

  assertNamesMatch(
    fixtures.homeCases.map((c) => c.name),
    requiredHomeCaseNames,
    "home-page homeCases",
  );
  const loadedHomeCases: LoadedHomeCase[] = [];
  let sawCappedCase = false;
  let sawUnsortedInput = false;
  for (const c of fixtures.homeCases) {
    assertExactKeys(c, homeCaseKeys, `home case ${c.name}`);
    for (const t of c.transcripts) {
      assertExactKeys(t, transcriptKeys, `home case ${c.name} transcript ${t.id}`);
      // An EMPTY hash is the malformed row this corpus deliberately models: the
      // wire contract guarantees the column, so a row without it is a server
      // contract violation the page must report rather than drop. Any other
      // shape is a typo in the fixture.
      if (t.projectHash !== "" && !/^[0-9a-f]{64}$/.test(t.projectHash)) {
        throw new Error(
          `home case ${c.name}: projectHash must be 64 lowercase hex chars, or empty to model a ` +
            `row that arrived without a project identity; got ${t.projectHash}`,
        );
      }
      if (Number.isNaN(Date.parse(t.publishedAt))) {
        throw new Error(
          `home case ${c.name}: transcript ${t.id} has an unparseable publishedAt ` +
            `${t.publishedAt}; the recent-first order is meaningless without a real timestamp`,
        );
      }
    }

    const ids = c.transcripts.map((t) => t.id);
    if (new Set(ids).size !== ids.length) {
      throw new Error(`home case ${c.name}: transcript ids must be unique`);
    }
    const titles = c.transcripts.map((t) => t.title);
    if (new Set(titles).size !== titles.length) {
      throw new Error(
        `home case ${c.name}: transcript titles must be unique, or an assertion on the rendered ` +
          `titles cannot tell one row from another`,
      );
    }

    if (c.expectEmptyState !== (c.transcripts.length === 0)) {
      throw new Error(
        `home case ${c.name}: expectEmptyState must be true exactly when the case supplies no ` +
          `transcripts`,
      );
    }

    // The expected recent list is the case's OWN rows in most-recent-first
    // order, truncated to the length the case declares. Derived from the
    // fixture, so it stays a statement about the data rather than a copy of
    // the page's sort.
    const wantRecent = [...c.transcripts]
      .sort((a, b) => Date.parse(b.publishedAt) - Date.parse(a.publishedAt))
      .slice(0, c.expectRecentTitles.length)
      .map((t) => t.title);
    if (JSON.stringify(c.expectRecentTitles) !== JSON.stringify(wantRecent)) {
      throw new Error(
        `home case ${c.name}: expectRecentTitles is ${c.expectRecentTitles.join(", ")} but the ` +
          `case's own rows, most recent first, are ${wantRecent.join(", ")}`,
      );
    }
    if (c.transcripts.length > c.expectRecentTitles.length) sawCappedCase = true;
    const givenOrder = c.transcripts.map((t) => t.title);
    if (c.transcripts.length > 1 && JSON.stringify(givenOrder) !== JSON.stringify(wantRecent)) {
      sawUnsortedInput = true;
    }

    // Project rows are the distinct hashes, most recently worked first — the
    // order the page's grouping produces, and the order that answers "what was
    // I working on". Derived here from the case's own timestamps.
    const malformedCount = c.transcripts.filter((t) => t.projectHash === "").length;
    loadedHomeCases.push({ ...c, malformedCount });

    const counts = new Map<string, number>();
    const names = new Map<string, string>();
    const latest = new Map<string, number>();
    for (const t of c.transcripts) {
      if (t.projectHash === "") continue;
      const at = Date.parse(t.publishedAt);
      if (!counts.has(t.projectHash)) names.set(t.projectHash, t.projectDisplayName);
      counts.set(t.projectHash, (counts.get(t.projectHash) ?? 0) + 1);
      latest.set(t.projectHash, Math.max(latest.get(t.projectHash) ?? at, at));
    }
    const seen = [...latest.keys()].sort((a, b) => latest.get(b)! - latest.get(a)!);
    if (c.expectProjectRows.length !== seen.length) {
      throw new Error(
        `home case ${c.name}: expects ${c.expectProjectRows.length} project rows but supplies ` +
          `${seen.length} distinct project hashes`,
      );
    }
    c.expectProjectRows.forEach((row, i) => {
      assertExactKeys(row, projectRowKeys, `home case ${c.name} project row ${row.displayName}`);
      const hash = seen[i];
      if (row.displayName !== names.get(hash)) {
        throw new Error(
          `home case ${c.name}: project row ${i} expects the name ${row.displayName} but its ` +
            `hash carries ${names.get(hash)}`,
        );
      }
      if (row.sessionCount !== counts.get(hash)) {
        throw new Error(
          `home case ${c.name}: project row ${row.displayName} expects ${row.sessionCount} ` +
            `sessions but the case supplies ${counts.get(hash)}`,
        );
      }
      if (!row.href.endsWith(`/${hash}`)) {
        throw new Error(
          `home case ${c.name}: project row ${row.displayName} must link to a path ending in its ` +
            `project hash, or the case cannot distinguish a hash-keyed link from a name-keyed one`,
        );
      }
      if (row.href !== `/users/${encodeURIComponent(c.viewerUsername)}/projects/${hash}`) {
        throw new Error(
          `home case ${c.name}: project row ${row.displayName} must link under the viewer's own ` +
            `profile, with the username percent-encoded; got ${row.href}`,
        );
      }
    });
  }

  // A page that dropped the anomaly notice, or silently folded an identity-less
  // row into a synthetic project, would pass a corpus in which every row is
  // well formed.
  if (!loadedHomeCases.some((c) => c.malformedCount > 0)) {
    throw new Error(
      `home-page homeCases: at least one case must supply a row with NO project identity. Without ` +
        `one, a page that dropped the anomaly notice, or grouped the row under a made-up project, ` +
        `would still pass.`,
    );
  }
  if (!sawCappedCase) {
    throw new Error(
      `home-page homeCases: at least one case must supply MORE transcripts than its recent list ` +
        `shows. Without one, a page that dropped the cap and listed everything would still pass.`,
    );
  }
  if (!sawUnsortedInput) {
    throw new Error(
      `home-page homeCases: at least one case must supply its transcripts in an order that is ` +
        `NOT already most-recent-first. Without one, a page that never sorted would still pass.`,
    );
  }

  return { ...fixtures, homeCases: loadedHomeCases };
}
