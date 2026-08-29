import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Why a case's response names a parent it does not carry.
 *
 * One list response is not the viewer's whole readable corpus, so a named
 * parent can be missing for three different reasons and the page cannot tell
 * them apart. All three must leave the row browsing normally, which is why the
 * fixtures must cover every one of them.
 */
export type AbsentParentCause = "none" | "another-page" | "filtered-out" | "not-visible";

const ABSENT_PARENT_CAUSES: readonly AbsentParentCause[] = [
  "none",
  "another-page",
  "filtered-out",
  "not-visible",
];

/**
 * A mounted route a case is asserted on.
 *
 * A closed set, so a case cannot name a surface this corpus does not assert and
 * quietly prove nothing. Every one of these lists folds with the SAME
 * implementation, and they still answer the question differently: discovery
 * folds a started session away and offers no control, because a browse card
 * names no parent for a count to hang off, while every other surface reveals
 * the started sessions from a control on the row that started them.
 */
export type ChildSessionSurface =
  | "explore"
  | "home"
  | "project"
  | "profile"
  | "collective-browse"
  | "collective-repos"
  | "pending-queue"
  | "my-contributions"
  | "contribute";

const CHILD_SESSION_SURFACES: readonly ChildSessionSurface[] = [
  "explore",
  "home",
  "project",
  "profile",
  "collective-browse",
  "collective-repos",
  "pending-queue",
  "my-contributions",
  "contribute",
];

/**
 * The surfaces whose list is scoped to one person, and so cannot carry rows
 * from two owners or the discovery-only agent scope.
 *
 * A person's library, their own contributions to a collective and the
 * contribute listing all answer with the caller's OWN transcripts. The
 * collective browse list, the repository view and the review queue hold
 * everybody's, which is exactly where a session id from one publisher must be
 * proven unable to capture another publisher's row.
 */
const OWNER_SCOPED_SURFACES: readonly ChildSessionSurface[] = [
  "home",
  "project",
  "profile",
  "my-contributions",
  "contribute",
];

/** One transcript in a mocked `/api/v1/transcripts` response. `name` is also
 *  the transcript id, so a failure names the case's own vocabulary. */
export type ChildSessionRow = {
  name: string;
  ownerID: string;
  localID: string;
  parentSessionID: string | null;
};

/** One collapsed group the mounted surface must render. */
export type ChildSessionExpectedGroup = {
  /** The row name of the browse row that started `children`. */
  parent: string;
  /** The literal text of the collapsed control. */
  label: string;
  /** The row names the control must reveal, and only those. */
  children: string[];
};

export type ChildSessionGroupingCase = {
  name: string;
  /** The mounted routes this case's expectations are asserted on. */
  surfaces: ChildSessionSurface[];
  absentParentCause: AbsentParentCause;
  rows: ChildSessionRow[];
  /** Transcript ids the same endpoint returns for `origin=agent`, which the
   *  server keeps out of the default list and counts separately. */
  agentSessions: string[];
  /** The total the server reports for the active filters. */
  serverTotal: number;
  /** The number the list header must show above the grid. */
  expectedVisibleCount: number;
  expectedRootRows: string[];
  /** The ordered rows the HOME list shows, on a `home` case; `[]` elsewhere.
   *  Home caps its list, so what it shows is not always every row that kept its
   *  place, and the ORDER is part of the answer: groups are ranked by the
   *  newest row in each, so a group holding the person's newest session cannot
   *  be cut off the page while that session is only reachable from inside it. */
  expectedHomeRows: string[];
  expectedGroups: ChildSessionExpectedGroup[];
};

export type ChildSessionGroupingFixtures = {
  cases: ChildSessionGroupingCase[];
};

/** Deletion guard: every named case must be present. Names rather than a row
 *  count, so adding a case is one edit to the fixture and a deleted case still
 *  fails loudly by name. */
const requiredCaseNames = [
  "parent-with-children-folds-to-the-parent-row",
  "child-whose-parent-the-viewer-cannot-read-keeps-its-own-row",
  "parent-on-another-page-leaves-the-child-a-plain-row",
  "search-matching-only-the-child-leaves-it-a-plain-row",
  "session-without-a-parent-renders-unchanged",
  "two-parents-each-keep-their-own-group",
  "a-session-id-from-another-owner-does-not-capture-a-row",
  "a-session-started-two-levels-down-joins-the-topmost-group",
  "a-chain-folds-around-the-highest-row-in-this-response",
  "a-session-that-names-itself-as-its-parent-stays-in-the-list",
  "a-ring-of-sessions-that-name-each-other-stays-in-the-list",
  "a-session-chaining-into-a-ring-keeps-its-own-row",
  "the-count-above-the-grid-excludes-folded-rows",
  "a-longer-result-set-keeps-the-server-count",
  "the-agent-group-and-a-child-group-sit-together",
  "a-started-session-is-listed-inside-its-parents-chip",
  "a-row-whose-parent-is-not-in-the-list-keeps-its-place",
  "home-shows-the-group-holding-the-newest-session",
  "a-collectives-contributions-read-a-started-session-under-its-starter",
  "a-collective-row-naming-a-session-this-page-omits-keeps-its-row",
  "a-review-queue-reads-a-started-submission-under-its-starter",
  "a-submission-whose-starter-was-not-offered-keeps-its-queue-row",
  "your-contributions-read-a-started-contribution-under-its-starter",
  "a-contribution-whose-starter-is-not-in-this-collective-keeps-its-row",
  "the-contribute-tree-nests-a-started-session-under-its-starter",
  "a-contributable-row-naming-an-unlisted-parent-keeps-its-own-place",
] as const;

const caseKeys = [
  "name",
  "surfaces",
  "absentParentCause",
  "rows",
  "agentSessions",
  "serverTotal",
  "expectedVisibleCount",
  "expectedRootRows",
  "expectedHomeRows",
  "expectedGroups",
];
const rowKeys = ["name", "ownerID", "localID", "parentSessionID"];
const groupKeys = ["parent", "label", "children"];

/** The chip's collapsed text: a bare count, with no leading `+`. The agent
 *  group beside it keeps its own `+`; the chip hangs off its own parent's row,
 *  where the count reads as part of that row. */
function expectedLabel(count: number): string {
  return `${count} child session${count === 1 ? "" : "s"}`;
}

export function loadChildSessionGroupingFixtures(): ChildSessionGroupingFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/child-session-grouping.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("child-session-grouping fixture root must be an object");
  }
  assertExactKeys(parsed, ["cases"], "fixture root");
  const fixtures = parsed as ChildSessionGroupingFixtures;

  assertNamesMatch(
    fixtures.cases.map(({ name }) => name),
    requiredCaseNames,
    "child-session-grouping",
  );

  const causes = new Set<string>();
  const surfacesCovered = new Set<ChildSessionSurface>();
  const surfacesShowingAGroup = new Set<ChildSessionSurface>();
  const surfacesKeepingARowWithAnAbsentParent = new Set<ChildSessionSurface>();
  let sawCrossOwnerRows = false;
  let sawFoldCorrectingTheCount = false;
  let sawLongerResultKeepingTheServerCount = false;
  let sawBothGroupsTogether = false;
  let sawHomeCappingItsList = false;
  let sawHomeLedByAGroupsNewestRow = false;

  for (const c of fixtures.cases) {
    assertExactKeys(c, caseKeys, `case ${c.name}`);
    if (!Array.isArray(c.surfaces) || c.surfaces.length === 0) {
      throw new Error(
        `case ${c.name}: surfaces must name at least one mounted route, or nothing asserts this case`,
      );
    }
    if (new Set(c.surfaces).size !== c.surfaces.length) {
      throw new Error(`case ${c.name}: a surface is named twice`);
    }
    for (const surface of c.surfaces) {
      if (!CHILD_SESSION_SURFACES.includes(surface)) {
        throw new Error(
          `case ${c.name}: surfaces must be drawn from ${CHILD_SESSION_SURFACES.join(", ")}, got ${surface}`,
        );
      }
      surfacesCovered.add(surface);
      if (c.expectedGroups.length > 0) surfacesShowingAGroup.add(surface);
      if (c.absentParentCause !== "none") surfacesKeepingARowWithAnAbsentParent.add(surface);
    }
    const ownerScoped = c.surfaces.filter((surface) => OWNER_SCOPED_SURFACES.includes(surface));
    if (ownerScoped.length > 0) {
      // Both owner-scoped surfaces read ONE person's sessions, so a case that
      // crossed owners there would describe a response the app cannot receive.
      if (new Set(c.rows.map((row) => row.ownerID)).size > 1) {
        throw new Error(
          `case ${c.name} is asserted on ${ownerScoped.join(", ")}, which show one person's own sessions, but its ` +
            `rows come from more than one owner`,
        );
      }
      if (c.agentSessions.length > 0) {
        throw new Error(
          `case ${c.name} is asserted on ${ownerScoped.join(", ")}, which never request the discovery-only ` +
            `origin=agent scope, so it cannot declare agent sessions`,
        );
      }
    }
    if (!ABSENT_PARENT_CAUSES.includes(c.absentParentCause)) {
      throw new Error(
        `case ${c.name}: absentParentCause must be one of ${ABSENT_PARENT_CAUSES.join(", ")}, got ` +
          `${c.absentParentCause}`,
      );
    }
    causes.add(c.absentParentCause);

    const rowNames = new Set<string>();
    const sessionKeys = new Set<string>();
    const owners = new Set<string>();
    for (const row of c.rows) {
      assertExactKeys(row, rowKeys, `case ${c.name} row ${row.name}`);
      if (rowNames.has(row.name)) {
        throw new Error(
          `case ${c.name}: row name ${row.name} appears twice; each row name is also its transcript id, so the ` +
            `assertions could not tell the two rows apart in the DOM`,
        );
      }
      rowNames.add(row.name);
      owners.add(row.ownerID);
      // The database holds `UNIQUE (owner_id, local_id)`; a fixture that breaks
      // it would describe a response the server cannot return.
      const sessionKey = `${row.ownerID}/${row.localID}`;
      if (sessionKeys.has(sessionKey)) {
        throw new Error(
          `case ${c.name}: owner ${row.ownerID} carries the session id ${row.localID} twice, which the database ` +
            `forbids; give one of the rows a different localID`,
        );
      }
      sessionKeys.add(sessionKey);
      if (row.parentSessionID !== null && typeof row.parentSessionID !== "string") {
        throw new Error(`case ${c.name} row ${row.name}: parentSessionID must be a session id or null`);
      }
    }
    if (owners.size > 1) sawCrossOwnerRows = true;

    // A case declares `none` only when every named parent is in the response,
    // and declares a cause only when some named parent is absent. Otherwise the
    // three absence causes could be "covered" by cases that never exercise one.
    const localIDsPresent = new Set(c.rows.map((row) => `${row.ownerID}/${row.localID}`));
    const hasAbsentParent = c.rows.some(
      (row) => row.parentSessionID !== null && !localIDsPresent.has(`${row.ownerID}/${row.parentSessionID}`),
    );
    if (hasAbsentParent !== (c.absentParentCause !== "none")) {
      throw new Error(
        `case ${c.name}: absentParentCause is ${c.absentParentCause}, but the rows ` +
          `${hasAbsentParent ? "do" : "do not"} name a parent this response omits`,
      );
    }

    // The two expectations must partition the rows: every row is a browse row
    // or is folded into exactly one group. No row is ever left out.
    const claimed: string[] = [...c.expectedRootRows];
    let foldedCount = 0;
    for (const group of c.expectedGroups) {
      assertExactKeys(group, groupKeys, `case ${c.name} group under ${group.parent}`);
      if (!c.expectedRootRows.includes(group.parent)) {
        throw new Error(
          `case ${c.name}: group parent ${group.parent} is not one of the browse rows; a group can only hang under a ` +
            `row the viewer can see`,
        );
      }
      if (group.children.length === 0) {
        throw new Error(`case ${c.name}: the group under ${group.parent} names no children, so no control would render`);
      }
      if (group.label !== expectedLabel(group.children.length)) {
        throw new Error(
          `case ${c.name}: the group under ${group.parent} expects the label ${group.label}, which does not describe ` +
            `its ${group.children.length} children`,
        );
      }
      claimed.push(...group.children);
      foldedCount += group.children.length;
    }

    const claimedSet = new Set(claimed);
    if (claimedSet.size !== claimed.length) {
      throw new Error(
        `case ${c.name}: a row is claimed twice across the browse rows and the groups; each row has exactly one outcome`,
      );
    }
    const missing = [...rowNames].filter((name) => !claimedSet.has(name));
    const unknown = [...claimedSet].filter((name) => !rowNames.has(name));
    if (missing.length > 0 || unknown.length > 0) {
      throw new Error(
        `case ${c.name}: the expectations do not cover the rows exactly; unclaimed rows ${missing.join(", ") || "none"}, ` +
          `expectations naming no row ${unknown.join(", ") || "none"}. Every row is a browse row or is folded into a ` +
          `group; the fold never leaves a row out`,
      );
    }

    if (c.serverTotal < c.rows.length) {
      throw new Error(
        `case ${c.name}: serverTotal ${c.serverTotal} is smaller than the ${c.rows.length} rows the response carries`,
      );
    }
    // The header count is corrected only when this response is the whole result
    // set. On a longer one the server's own total stands, because the same
    // number drives the pager.
    const wholeResultSet = c.serverTotal <= c.rows.length;
    const wantVisibleCount = wholeResultSet ? c.expectedRootRows.length : c.serverTotal;
    if (c.expectedVisibleCount !== wantVisibleCount) {
      throw new Error(
        `case ${c.name}: the header count must be ${
          wholeResultSet
            ? `the ${c.expectedRootRows.length} browse rows, because this response is the whole result set`
            : `the server's total ${c.serverTotal}, because the result set is longer than this response`
        }; got ${c.expectedVisibleCount}`,
      );
    }
    if (wholeResultSet && foldedCount > 0 && c.expectedVisibleCount !== c.serverTotal) {
      sawFoldCorrectingTheCount = true;
    }
    if (!wholeResultSet && foldedCount > 0 && c.expectedVisibleCount === c.serverTotal) {
      sawLongerResultKeepingTheServerCount = true;
    }

    // What home SHOWS is its own expectation, because home caps its list and
    // ranks groups by the newest row in each. Only a `home` case may state it.
    const onHome = c.surfaces.includes("home");
    if (!onHome && c.expectedHomeRows.length > 0) {
      throw new Error(
        `case ${c.name}: expectedHomeRows names rows but the case is not asserted on home, so nothing reads it`,
      );
    }
    if (onHome) {
      if (c.expectedHomeRows.length === 0) {
        throw new Error(
          `case ${c.name} is asserted on home but names no rows for it; home's list is what the case is about`,
        );
      }
      if (new Set(c.expectedHomeRows).size !== c.expectedHomeRows.length) {
        throw new Error(`case ${c.name}: a row is named twice in expectedHomeRows`);
      }
      const notARootRow = c.expectedHomeRows.filter((name) => !c.expectedRootRows.includes(name));
      if (notARootRow.length > 0) {
        throw new Error(
          `case ${c.name}: expectedHomeRows names ${notARootRow.join(", ")}, which ${
            notARootRow.length === 1 ? "is" : "are"
          } not among the rows that kept their place; home can only show a row the fold left in the list`,
        );
      }
      if (c.expectedHomeRows.length < c.expectedRootRows.length) sawHomeCappingItsList = true;
      if (c.expectedHomeRows[0] !== c.expectedRootRows[0]) sawHomeLedByAGroupsNewestRow = true;
    }

    const agentIDs = new Set(c.agentSessions);
    if (agentIDs.size !== c.agentSessions.length) {
      throw new Error(`case ${c.name}: an agent-session id appears twice`);
    }
    for (const id of c.agentSessions) {
      if (rowNames.has(id)) {
        throw new Error(
          `case ${c.name}: ${id} is both a browse row and an agent session; the server serves the two scopes ` +
            `separately, so a case cannot claim the same id for both`,
        );
      }
    }
    if (c.agentSessions.length > 0 && c.expectedGroups.length > 0) sawBothGroupsTogether = true;
  }

  for (const surface of CHILD_SESSION_SURFACES) {
    if (!surfacesCovered.has(surface)) {
      throw new Error(
        `child-session-grouping fixtures assert no case on ${surface}: each mounted route answers the started-session ` +
          `question differently, so each needs its own evidence`,
      );
    }
    if (!surfacesShowingAGroup.has(surface)) {
      throw new Error(
        `child-session-grouping fixtures cover no case on ${surface} where a session started another: on discovery ` +
          `that is the fold itself, and on every other surface it is the control on the parent's row, so a corpus ` +
          `without one proves nothing about ${surface}`,
      );
    }
    if (!surfacesKeepingARowWithAnAbsentParent.has(surface)) {
      throw new Error(
        `child-session-grouping fixtures cover no case on ${surface} where a row names a session the response does ` +
          `not carry: that row must keep its ordinary place, and a build that folded it into nothing -- or dropped ` +
          `it -- would take a readable session off ${surface} with nothing failing`,
      );
    }
  }
  if (!sawCrossOwnerRows) {
    throw new Error(
      "child-session-grouping fixtures cover no case with rows from two owners: session ids are unique per owner, so " +
        "matching across owners must be proven wrong",
    );
  }
  if (!sawFoldCorrectingTheCount) {
    throw new Error(
      "child-session-grouping fixtures cover no case where folding changes the header count: a count that still " +
        "counts folded rows disagrees with the cards under it, which is the regression this expectation guards",
    );
  }
  if (!sawLongerResultKeepingTheServerCount) {
    throw new Error(
      "child-session-grouping fixtures cover no result set longer than one response that also folds rows away: " +
        "correcting the count per page would shrink the number the pager reads and could end it early, so the case " +
        "that keeps the server's own count must be covered",
    );
  }
  if (!sawBothGroupsTogether) {
    throw new Error(
      "child-session-grouping fixtures cover no page carrying both collapsed groups: the agent group and a child " +
        "group render into the same column, and neither may take rows from the other",
    );
  }
  if (!sawHomeCappingItsList) {
    throw new Error(
      "child-session-grouping fixtures cover no home case with more rows than home shows: the list is capped, so a " +
        "corpus that never reaches the cap cannot tell a correct cut from one that drops the wrong rows",
    );
  }
  if (!sawHomeLedByAGroupsNewestRow) {
    throw new Error(
      "child-session-grouping fixtures cover no home case whose first shown row is not the first row the server " +
        "sent: home ranks a group by the NEWEST row in it, so without a case where that ranking moves a row, a " +
        "build that ranked parents by their own timestamps alone would pass, and could cut the group holding the " +
        "person's newest session off the page",
    );
  }
  for (const cause of ABSENT_PARENT_CAUSES) {
    if (!causes.has(cause)) {
      throw new Error(
        `child-session-grouping fixtures cover no ${cause} case: a parent can be missing from a response because it ` +
          `is on another page, because a filter excluded it, or because the viewer may not read it, and the page ` +
          `cannot tell those apart, so every cause must be proven to leave the row browsing normally`,
      );
    }
  }

  return fixtures;
}
