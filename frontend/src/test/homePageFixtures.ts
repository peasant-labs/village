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

/** One pull request reference as a fixture writes it: `owner/name` and a number. */
export type HomePullRequestRefCase = {
  repo: string;
  number: number;
};

export type HomePullRequestsCase = {
  count: number;
  recent: HomePullRequestRefCase[];
};

export type HomeTranscriptCase = {
  id: string;
  title: string;
  projectHash: string;
  projectDisplayName: string;
  publishedAt: string;
  /** The session id the recording harness used. Defaults to the row's own id.
   *  Only a case about started sessions has to say it. */
  localID?: string;
  /** The harness id of the session that started this one, or null when nothing
   *  did. Absent in `home-page.yaml`, whose cases are not about parentage; the
   *  started-session corpus supplies it. */
  parentSessionID?: string | null;
  /** The row's pull request summary. Absent means none. */
  pullRequests?: HomePullRequestsCase;
  /** The collectives that approved the row. Absent means none. */
  sharedWith?: string[];
  /** The row's own visibility. Absent means private. */
  visibility?: "private" | "shared" | "public";
};

/** How the owner-scoped list request behaves for a case. */
export type HomeRequestFailure = "never" | "always" | "after-first-answer";

/**
 * The answers the home page can land on. A closed set, so a case cannot name a
 * seventh surface and quietly assert nothing.
 */
export type HomeSurface =
  | "rows"
  | "empty"
  | "failure"
  | "stale"
  | "skeleton"
  | "no-handle";

export type HomeStatsCase = {
  transcripts: number;
  turns: number;
  durationMs: number;
  tokens: number;
  pullRequests: number;
};

export type HomeCollectiveCase = {
  id: string;
  name: string;
  role: "owner" | "member" | "contributor";
  members: number;
};

export type HomeContributionCase = {
  id: string;
  name: string;
  pending: number;
};

/** What the pull requests column does past the numbers it shows. */
export type HomePullRequestMore = "none" | "reveal" | "link";

export type HomePullRequestCellCase = {
  title: string;
  /** The numbers shown before any `+N`, as they read (`#42`). */
  shown: string[];
  more: HomePullRequestMore;
};

export type HomeCase = {
  name: string;
  viewerUsername: string;
  transcripts: HomeTranscriptCase[];
  /** Whether the account claims a chosen handle. */
  usernameChosen: boolean;
  requestFailure: HomeRequestFailure;
  expectHomeSurface: HomeSurface;
  expectRowTitles: string[];
  expectProjectLinks: { title: string; href: string | null }[];
  stats?: HomeStatsCase;
  expectStats?: string[];
  collectives?: HomeCollectiveCase[];
  expectRailCollectives?: { name: string; meta: string }[];
  contributions?: HomeContributionCase[];
  expectWaiting?: string[];
  search?: { query: string; expectRowTitles: string[] };
  loadMore?: { expectRowTitles: string[] };
  expectPullRequestCells?: HomePullRequestCellCase[];
  expectSharedWithCells?: { title: string; text: string[] }[];
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
   *  an anomaly and listed without a project link. */
  malformedCount: number;
};

export type HomeViewerChangeCase = {
  name: string;
  firstViewer: string;
  secondViewer: string;
};

export type HomeSortRow = {
  id: string;
  publishedAt: string;
};

export type HomeSortCase = {
  name: string;
  given: HomeSortRow[];
  expectOrder: string[];
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
  sortCases: HomeSortCase[];
  viewerChangeCases: HomeViewerChangeCase[];
};

export type HomePageFixtures = {
  routeCases: HomeRouteCase[];
  navCases: HomeNavCase[];
  homeCases: LoadedHomeCase[];
  sortCases: HomeSortCase[];
  viewerChangeCases: HomeViewerChangeCase[];
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
  "public-link-access-is-named-alongside-approved-collectives",
  "pending-private-submission-does-not-claim-author-only-access",
  "more-sessions-than-the-recent-list-shows-are-capped",
  "a-person-with-nothing-published-gets-the-teaching-empty-state",
  "a-username-needing-escaping-still-links-to-its-project",
  "a-row-arriving-without-a-project-identity-is-reported-not-dropped",
  "a-failed-request-is-not-an-empty-library",
  "a-failed-refresh-keeps-the-rows-it-already-had",
  "a-handle-still-being-chosen-asks-for-nothing",
  "a-chosen-handle-that-is-blank-says-so",
  "the-stats-line-states-the-personal-totals",
  "a-search-lists-only-the-matching-transcripts",
  "a-search-that-matches-nothing-says-so",
  "the-pull-request-column-with-none-one-and-several",
  "transcripts-waiting-for-approval-are-listed-by-collective",
  "nothing-waiting-draws-no-waiting-section",
  "the-rail-lists-your-collectives",
] as const;

const routeCaseKeys = ["name", "path", "viewerUsername", "expectSurface"];
const transcriptKeys = ["id", "title", "projectHash", "projectDisplayName", "publishedAt"];
const optionalTranscriptKeys = ["pullRequests", "sharedWith", "visibility"];
const homeCaseKeys = [
  "name",
  "viewerUsername",
  "transcripts",
  "usernameChosen",
  "requestFailure",
  "expectHomeSurface",
  "expectRowTitles",
  "expectProjectLinks",
];
const optionalHomeCaseKeys = [
  "stats",
  "expectStats",
  "collectives",
  "expectRailCollectives",
  "contributions",
  "expectWaiting",
  "search",
  "loadMore",
  "expectPullRequestCells",
  "expectSharedWithCells",
];

/** Like {@link assertExactKeys}, for a row whose optional fields may be absent:
 *  every required key present, and nothing outside required ∪ optional. */
function assertKeysWithin(
  value: object,
  required: string[],
  optional: string[],
  location: string,
): void {
  const keys = Object.keys(value);
  const missing = required.filter((k) => !keys.includes(k));
  const unknown = keys.filter((k) => !required.includes(k) && !optional.includes(k));
  if (missing.length > 0 || unknown.length > 0) {
    throw new Error(
      `${location} has unknown or missing fields: missing ${missing.join(", ") || "none"}; ` +
        `unknown ${unknown.join(", ") || "none"}`,
    );
  }
}

/** Optional fields that only mean something as a pair: a served input and
 *  what the page must show for it. One without the other is a dead field. */
const pairedFields: ReadonlyArray<readonly [keyof HomeCase, keyof HomeCase]> = [
  ["stats", "expectStats"],
  ["contributions", "expectWaiting"],
];

const requestFailures: readonly HomeRequestFailure[] = [
  "never",
  "always",
  "after-first-answer",
];

const homeSurfaces: readonly HomeSurface[] = [
  "rows",
  "empty",
  "failure",
  "stale",
  "skeleton",
  "no-handle",
];

const pullRequestMores: readonly HomePullRequestMore[] = ["none", "reveal", "link"];

/**
 * The surface a case's OWN inputs entail. Derived here, so a case cannot claim
 * an expectation its inputs do not support, and so each rule is stated once.
 *
 * The order below mirrors the early returns in `HomePage`, and deliberately:
 * it is a second copy of that precedence, kept so a case that contradicts
 * itself fails loudly at load. If the page ever reorders its branches — a
 * failure taking the page ahead of the no-handle answer, say — this function
 * moves with it, and the mounted assertions are what catch the mismatch.
 */
function surfaceFor(c: HomeCase): HomeSurface {
  if (c.viewerUsername === "") return c.usernameChosen ? "no-handle" : "skeleton";
  if (c.requestFailure === "always") return "failure";
  if (c.requestFailure === "after-first-answer") return "stale";
  return c.transcripts.length === 0 ? "empty" : "rows";
}

/** A case's rows, most recently published first — the order the table lists
 *  them in. Derived from the fixture, never from the page's sort. */
function newestFirstTitles(rows: HomeTranscriptCase[]): string[] {
  return [...rows]
    .sort((a, b) => Date.parse(b.publishedAt) - Date.parse(a.publishedAt))
    .map((t) => t.title);
}

const surfaces: readonly HomeRouteSurface[] = ["home", "explore"];

const requiredSortCaseNames = [
  "newest-first-among-parseable-timestamps",
  "an-unparseable-timestamp-sorts-last-instead-of-throwing",
  "two-unparseable-timestamps-keep-the-order-they-arrived-in",
] as const;

const sortCaseKeys = ["name", "given", "expectOrder"];

const requiredViewerChangeCaseNames = [
  "a-new-handle-does-not-inherit-the-previous-handle-failure",
] as const;

const viewerChangeCaseKeys = ["name", "firstViewer", "secondViewer"];
const sortRowKeys = ["id", "publishedAt"];

export function loadHomePageFixtures(): HomePageFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/home-page.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("home-page fixture root must be an object");
  }
  assertExactKeys(
    parsed,
    ["routeCases", "navCases", "homeCases", "sortCases", "viewerChangeCases"],
    "fixture root",
  );
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
    assertKeysWithin(c, homeCaseKeys, optionalHomeCaseKeys, `home case ${c.name}`);
    for (const t of c.transcripts) {
      assertKeysWithin(t, transcriptKeys, optionalTranscriptKeys, `home case ${c.name} transcript ${t.id}`);
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
      if (t.pullRequests !== undefined) {
        assertExactKeys(t.pullRequests, ["count", "recent"], `home case ${c.name} ${t.id} pullRequests`);
        // The contract carries at most three references, and never more than
        // it counts.
        if (t.pullRequests.recent.length > 3 || t.pullRequests.recent.length > t.pullRequests.count) {
          throw new Error(
            `home case ${c.name}: ${t.id} carries ${t.pullRequests.recent.length} pull request ` +
              `references for a count of ${t.pullRequests.count}; a row carries at most three, and ` +
              `never more than it counts`,
          );
        }
        for (const ref of t.pullRequests.recent) {
          assertExactKeys(ref, ["repo", "number"], `home case ${c.name} ${t.id} pull request`);
          if (!/^[^/\s]+\/[^/\s]+$/.test(ref.repo)) {
            throw new Error(`home case ${c.name}: pull request repo ${ref.repo} must read owner/name`);
          }
        }
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

    if (!requestFailures.includes(c.requestFailure)) {
      throw new Error(
        `home case ${c.name}: ${c.requestFailure} is not a request behaviour. The closed set is ` +
          `${requestFailures.join(", ")}.`,
      );
    }
    if (!homeSurfaces.includes(c.expectHomeSurface)) {
      throw new Error(
        `home case ${c.name}: ${c.expectHomeSurface} is not a home surface. The closed set is ` +
          `${homeSurfaces.join(", ")}.`,
      );
    }
    // The distinction this corpus exists to hold: an answered request with no
    // rows is the empty library; a request that FAILED is not, and a page that
    // conflated them would tell somebody with a full shelf that it is bare.
    // A refresh that fails after rows arrived is a third answer again: the rows
    // stay, and only a notice above them changes.
    const entailed = surfaceFor(c);
    if (c.expectHomeSurface !== entailed) {
      throw new Error(
        `home case ${c.name}: expectHomeSurface is ${c.expectHomeSurface}, but the case's own ` +
          `inputs entail ${entailed}. Fix the expectation rather than the rule.`,
      );
    }
    // A request that never answers cannot also deliver rows, and a refresh has
    // nothing to keep unless rows arrived first.
    if (c.requestFailure === "always" && c.transcripts.length > 0) {
      throw new Error(
        `home case ${c.name}: a request that always fails cannot also deliver rows`,
      );
    }
    if (c.requestFailure === "after-first-answer" && c.transcripts.length === 0) {
      throw new Error(
        `home case ${c.name}: a failed REFRESH is only distinguishable from a failed first ` +
          `request when rows arrived first; supply at least one transcript`,
      );
    }
    if (c.viewerUsername === "" && c.transcripts.length > 0) {
      throw new Error(
        `home case ${c.name}: no request is issued without a handle, so the case cannot supply ` +
          `rows for one`,
      );
    }
    // A surface that renders no list cannot be asserted against row
    // expectations, so carrying them would leave dead fields nothing checks.
    const listless = c.expectHomeSurface !== "rows" && c.expectHomeSurface !== "stale";
    const optionalPresent = optionalHomeCaseKeys.filter((k) => k in c);
    if (
      listless &&
      (c.expectRowTitles.length > 0 || c.expectProjectLinks.length > 0 || optionalPresent.length > 0)
    ) {
      throw new Error(
        `home case ${c.name}: the ${c.expectHomeSurface} surface renders no table, so its row and ` +
          `feature expectations would never be read; leave them empty or absent`,
      );
    }
    for (const [input, expectation] of pairedFields) {
      if ((input in c) !== (expectation in c)) {
        throw new Error(
          `home case ${c.name}: ${String(input)} and ${String(expectation)} come as a pair; one ` +
            `without the other is a served input nothing checks, or an expectation nothing serves`,
        );
      }
    }
    if ("expectRailCollectives" in c && !("collectives" in c)) {
      throw new Error(`home case ${c.name}: expectRailCollectives needs the collectives it names`);
    }

    // The table lists the case's OWN rows newest first, truncated to the length
    // the case declares. Derived from the fixture, so it stays a statement about
    // the data rather than a copy of the page's sort.
    const newest = newestFirstTitles(c.transcripts);
    const wantFirst = newest.slice(0, c.expectRowTitles.length);
    if (JSON.stringify(c.expectRowTitles) !== JSON.stringify(wantFirst)) {
      throw new Error(
        `home case ${c.name}: expectRowTitles is ${c.expectRowTitles.join(", ")} but the case's ` +
          `own rows, most recent first, are ${wantFirst.join(", ")}`,
      );
    }
    if (c.transcripts.length > c.expectRowTitles.length) {
      sawCappedCase = true;
      // The rows past the first page are only asserted by pressing `load more`.
      // A case that holds them back without that step says nothing about them.
      if (c.loadMore === undefined) {
        throw new Error(
          `home case ${c.name}: supplies more rows than its table first lists, so it must press ` +
            `load more (loadMore) and say what the table lists after`,
        );
      }
      // The first page must BE the newest rows as served: the mock serves rows
      // in the order written, a page at a time.
      const servedFirst = c.transcripts.slice(0, c.expectRowTitles.length).map((t) => t.title);
      if (JSON.stringify([...servedFirst].sort()) !== JSON.stringify([...wantFirst].sort())) {
        throw new Error(
          `home case ${c.name}: its first ${c.expectRowTitles.length} rows as written are not its ` +
            `newest ones, so the first page served could not list what expectRowTitles says`,
        );
      }
    }
    if (c.loadMore !== undefined) {
      assertExactKeys(c.loadMore, ["expectRowTitles"], `home case ${c.name} loadMore`);
      if (JSON.stringify(c.loadMore.expectRowTitles) !== JSON.stringify(newest)) {
        throw new Error(
          `home case ${c.name}: after load more the table lists every row, newest first ` +
            `(${newest.join(", ")}); got ${c.loadMore.expectRowTitles.join(", ")}`,
        );
      }
    }
    const givenOrder = c.transcripts.map((t) => t.title);
    if (c.transcripts.length > 1 && JSON.stringify(givenOrder) !== JSON.stringify(newest)) {
      sawUnsortedInput = true;
    }

    // Where each named row's project links: under the viewer's own profile,
    // percent-encoded, keyed on the hash; or nowhere, for a row with no hash.
    for (const link of c.expectProjectLinks) {
      assertExactKeys(link, ["title", "href"], `home case ${c.name} project link`);
      const row = c.transcripts.find((t) => t.title === link.title);
      if (row === undefined) {
        throw new Error(`home case ${c.name}: project link names ${link.title}, which is not a row`);
      }
      const want =
        row.projectHash === ""
          ? null
          : `/users/${encodeURIComponent(c.viewerUsername)}/projects/${row.projectHash}`;
      if (link.href !== want) {
        throw new Error(
          `home case ${c.name}: ${link.title} must link its project to ${want ?? "nothing"}; got ` +
            `${link.href ?? "nothing"}`,
        );
      }
    }

    if (c.search !== undefined) {
      assertExactKeys(c.search, ["query", "expectRowTitles"], `home case ${c.name} search`);
      // What the mock server matches: the query anywhere in the title, ignoring
      // case, as the list handler's ILIKE does.
      const q = c.search.query.toLowerCase();
      const want = newestFirstTitles(c.transcripts.filter((t) => t.title.toLowerCase().includes(q)));
      if (JSON.stringify(c.search.expectRowTitles) !== JSON.stringify(want)) {
        throw new Error(
          `home case ${c.name}: searching "${c.search.query}" lists ${want.join(", ") || "nothing"}; ` +
            `got ${c.search.expectRowTitles.join(", ") || "nothing"}`,
        );
      }
    }

    for (const cell of c.expectPullRequestCells ?? []) {
      assertExactKeys(cell, ["title", "shown", "more"], `home case ${c.name} pull request cell`);
      const row = c.transcripts.find((t) => t.title === cell.title);
      if (row === undefined) {
        throw new Error(`home case ${c.name}: pull request cell names ${cell.title}, which is not a row`);
      }
      if (!pullRequestMores.includes(cell.more)) {
        throw new Error(`home case ${c.name}: ${cell.more} is not one of ${pullRequestMores.join(", ")}`);
      }
      const count = row.pullRequests?.count ?? 0;
      const carried = row.pullRequests?.recent ?? [];
      // Which `+N` a row's summary entails: none when everything it counts is
      // shown, an in-place reveal when it carries every reference, and a link
      // to the transcript page when it counts more than it carries.
      const shownCount = cell.shown.length;
      const entailedMore: HomePullRequestMore =
        count <= shownCount ? "none" : count <= carried.length ? "reveal" : "link";
      if (cell.more !== entailedMore) {
        throw new Error(
          `home case ${c.name}: ${cell.title} counts ${count} pull requests, carries ` +
            `${carried.length} and shows ${shownCount}, which entails ${entailedMore}; got ${cell.more}`,
        );
      }
      const wantShown = carried.slice(0, shownCount).map((ref) => `#${ref.number}`);
      if (JSON.stringify(cell.shown) !== JSON.stringify(wantShown)) {
        throw new Error(
          `home case ${c.name}: ${cell.title} shows its newest references first: ` +
            `${wantShown.join(", ")}; got ${cell.shown.join(", ")}`,
        );
      }
    }

    for (const cell of c.expectSharedWithCells ?? []) {
      assertExactKeys(cell, ["title", "text"], `home case ${c.name} shared with cell`);
      const row = c.transcripts.find((t) => t.title === cell.title);
      if (row === undefined) {
        throw new Error(`home case ${c.name}: shared with cell names ${cell.title}, which is not a row`);
      }
      if (cell.text.length === 0 || cell.text.some((label) => typeof label !== "string" || label === "")) {
        throw new Error(`home case ${c.name}: audience expectations must name the visible labels`);
      }
    }

    if (c.collectives !== undefined) {
      for (const collective of c.collectives) {
        assertExactKeys(collective, ["id", "name", "role", "members"], `home case ${c.name} collective`);
      }
    }
    if (c.expectRailCollectives !== undefined) {
      const names = (c.collectives ?? []).map((collective) => collective.name);
      const listed = c.expectRailCollectives.map((row) => {
        assertExactKeys(row, ["name", "meta"], `home case ${c.name} rail collective`);
        return row.name;
      });
      if (JSON.stringify(listed) !== JSON.stringify(names)) {
        throw new Error(
          `home case ${c.name}: the rail names the collectives in the order served ` +
            `(${names.join(", ")}); got ${listed.join(", ")}`,
        );
      }
    }
    if (c.contributions !== undefined) {
      for (const contribution of c.contributions) {
        assertExactKeys(contribution, ["id", "name", "pending"], `home case ${c.name} contribution`);
      }
      const waiting = c.contributions.filter((x) => x.pending > 0).map((x) => x.name);
      if (JSON.stringify(c.expectWaiting) !== JSON.stringify(waiting)) {
        throw new Error(
          `home case ${c.name}: the waiting section names the collectives holding something ` +
            `(${waiting.join(", ") || "none"}); got ${(c.expectWaiting ?? []).join(", ") || "none"}`,
        );
      }
    }
    if (c.stats !== undefined) {
      assertExactKeys(
        c.stats,
        ["transcripts", "turns", "durationMs", "tokens", "pullRequests"],
        `home case ${c.name} stats`,
      );
    }

    const malformedCount = c.transcripts.filter((t) => t.projectHash === "").length;
    loadedHomeCases.push({ ...c, malformedCount });
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
      `home-page homeCases: at least one case must supply MORE transcripts than its table first ` +
        `lists. Without one, a page that asked for everything at once, or never offered the rest, ` +
        `would still pass.`,
    );
  }
  // Each answer the page can give needs at least one case, or a page that
  // collapsed two of them together would still pass. The reasons differ, so
  // they are named one at a time rather than counted.
  const requiredSurfaces: ReadonlyArray<readonly [HomeSurface, string]> = [
    [
      "failure",
      `a page that rendered a failed request as the teaching empty state would still pass, and ` +
        `would tell a person with a full library that it is empty`,
    ],
    [
      "stale",
      `a page that replaced the rows it already holds with an error panel, because a LATER ` +
        `request failed, would still pass`,
    ],
    [
      "skeleton",
      `a page that issued a blank owner filter while the handle is still being chosen would still ` +
        `pass, and the list handler drops that filter, so the whole commons would arrive under a ` +
        `heading that says "your"`,
    ],
    [
      "no-handle",
      `a page that shimmered forever for an account whose chosen handle is blank would still pass`,
    ],
  ];
  for (const [surface, why] of requiredSurfaces) {
    if (!loadedHomeCases.some((c) => c.expectHomeSurface === surface)) {
      throw new Error(
        `home-page homeCases: at least one case must land on the ${surface} surface. Without one, ` +
          `${why}.`,
      );
    }
  }
  if (!sawUnsortedInput) {
    throw new Error(
      `home-page homeCases: at least one case must supply its transcripts in an order that is ` +
        `NOT already most-recent-first. Without one, a page that never sorted would still pass.`,
    );
  }
  // Each answer the new parts of the page can give needs a case too.
  const cells = loadedHomeCases.flatMap((c) => c.expectPullRequestCells ?? []);
  for (const more of pullRequestMores) {
    if (!cells.some((cell) => cell.more === more)) {
      throw new Error(`home-page homeCases: no pull request cell ends in ${more}`);
    }
  }
  if (!cells.some((cell) => cell.shown.length === 0) || !cells.some((cell) => cell.shown.length === 1)) {
    throw new Error("home-page homeCases: the pull request column needs a row with none and a row with one");
  }
  const searches = loadedHomeCases.flatMap((c) => (c.search ? [c.search] : []));
  if (!searches.some((q) => q.expectRowTitles.length > 0) || !searches.some((q) => q.expectRowTitles.length === 0)) {
    throw new Error("home-page homeCases: search needs a case that matches rows and one that matches none");
  }
  const waitingCases = loadedHomeCases.filter((c) => c.expectWaiting !== undefined);
  if (
    !waitingCases.some((c) => c.expectWaiting!.length > 0) ||
    !waitingCases.some((c) => c.expectWaiting!.length === 0)
  ) {
    throw new Error("home-page homeCases: the waiting section needs a case with something waiting and one with nothing");
  }
  if (!loadedHomeCases.some((c) => c.expectStats !== undefined)) {
    throw new Error("home-page homeCases: no case states the stats line");
  }
  if (!loadedHomeCases.some((c) => (c.expectRailCollectives ?? []).length > 0)) {
    throw new Error("home-page homeCases: no case lists the collectives rail");
  }

  assertNamesMatch(
    fixtures.sortCases.map((c) => c.name),
    requiredSortCaseNames,
    "home-page sortCases",
  );
  let sawUnparseable = false;
  for (const c of fixtures.sortCases) {
    assertExactKeys(c, sortCaseKeys, `sort case ${c.name}`);
    for (const row of c.given) {
      assertExactKeys(row, sortRowKeys, `sort case ${c.name} row ${row.id}`);
      if (Number.isNaN(Date.parse(row.publishedAt))) sawUnparseable = true;
    }
    const ids = c.given.map((r) => r.id);
    if (new Set(ids).size !== ids.length) {
      throw new Error(`sort case ${c.name}: row ids must be unique`);
    }
    if (JSON.stringify([...ids].sort()) !== JSON.stringify([...c.expectOrder].sort())) {
      throw new Error(
        `sort case ${c.name}: expectOrder must be a permutation of the rows the case supplies; ` +
          `got ${c.expectOrder.join(", ")} for rows ${ids.join(", ")}`,
      );
    }
  }
  // The mounted corpus cannot carry a bad timestamp, so if this group loses its
  // unparseable rows the branch that keeps one from throwing goes uncovered and
  // nothing says so.
  if (!sawUnparseable) {
    throw new Error(
      `home-page sortCases: at least one case must supply a timestamp that does NOT parse. It is ` +
        `the only place the sort's bad-value handling can be reached.`,
    );
  }

  assertNamesMatch(
    fixtures.viewerChangeCases.map((c) => c.name),
    requiredViewerChangeCaseNames,
    "home-page viewerChangeCases",
  );
  for (const c of fixtures.viewerChangeCases) {
    assertExactKeys(c, viewerChangeCaseKeys, `viewer change case ${c.name}`);
    if (c.firstViewer === c.secondViewer) {
      throw new Error(
        `viewer change case ${c.name}: the two handles must differ, or the case cannot tell a ` +
          `memory keyed on its owner from one that is not`,
      );
    }
    if (c.firstViewer === "" || c.secondViewer === "") {
      throw new Error(
        `viewer change case ${c.name}: both handles must be real; a blank one is answered by the ` +
          `no-handle surface instead and never reaches the list request`,
      );
    }
  }

  return { ...fixtures, homeCases: loadedHomeCases };
}
