import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import type { NameSource } from "@/lib/types";

/**
 * Loader for `src/testdata/project-page.yaml` — the case corpus behind
 * `src/projectPage.test.tsx`.
 *
 * Deletion protection is a required-NAME manifest per group. A deleted case
 * fails the loader because its name goes missing from the declared set, not
 * because a tally shrinks: a count guard churns on every legitimate addition
 * and conflicts whenever two changes append at once.
 */

export type ProjectPageViewerCase = {
  name: string;
  ownerUsername: string;
  viewerUsername: string | null;
  expectRenameControl: boolean;
};

export type ProjectPageNotFoundCase = {
  name: string;
  serverMessage: string;
};

export type ProjectPageRollupCollective = {
  id: string;
  name: string;
  description: string | null;
  linkedGithubOrg: string | null;
  transcriptCount: number;
};

export type ProjectPageRollupCase = {
  name: string;
  ownerUsername: string;
  viewerUsername: string;
  collectives: ProjectPageRollupCollective[];
  expectedRowNames: string[];
  expectedCounts: string[];
  expectEmptyRollup: boolean;
};

export type ProjectPageRenameCase = {
  name: string;
  initialDisplayName: string;
  initialSource: NameSource;
  action: "set" | "clear" | "none";
  typedName: string | null;
  expectedMethod: "PATCH" | "DELETE" | null;
  expectedPathSuffix: string | null;
  expectedBodyDisplayName: string | null;
  resultingDisplayName: string;
  resultingSource: NameSource;
  expectClearEnabledBefore: boolean;
};

export type ProjectPageHeaderCase = {
  name: string;
  remoteLabel: string;
  expectedSubtitle: string | null;
};

export type ProjectPageProfileLinkCase = {
  name: string;
  ownerUsername: string;
  projectHash: string;
  projectDisplayName: string;
  expectedHref: string;
};

export type CommonsCrumbRoute = "profile" | "project";

/** Whether the route renders its ordinary state or its not-found state. Both
 *  render a way back to the commons, and both have to point at the same place. */
export type CommonsCrumbState = "found" | "missing";

export type ProjectPageCommonsCrumbCase = {
  name: string;
  route: CommonsCrumbRoute;
  state: CommonsCrumbState;
  /** Every commons-labelled link this state renders, in render order. */
  expectedLabels: string[];
};

export type ProjectPageFixtures = {
  viewerCases: ProjectPageViewerCase[];
  notFoundCases: ProjectPageNotFoundCase[];
  rollupCases: ProjectPageRollupCase[];
  renameCases: ProjectPageRenameCase[];
  headerCases: ProjectPageHeaderCase[];
  profileLinkCases: ProjectPageProfileLinkCase[];
  commonsCrumbCases: ProjectPageCommonsCrumbCase[];
};

const requiredViewerCaseNames = [
  "owner-sees-the-rename-control",
  "other-signed-in-viewer-sees-no-rename-control",
  "anonymous-viewer-sees-no-rename-control",
  "owner-with-different-letter-case-still-sees-the-control",
] as const;

const requiredNotFoundCaseNames = [
  "hidden-owner-renders-not-found",
  "no-such-user-renders-not-found",
  "no-such-project-for-a-visible-owner-renders-not-found",
] as const;

const requiredRollupCaseNames = [
  "approved-collectives-render-with-their-transcript-counts",
  "empty-rollup-renders-as-an-ordinary-empty-state",
] as const;

const requiredRenameCaseNames = [
  "saving-a-name-sends-the-hash-and-the-new-name",
  "clearing-reverts-to-the-resolved-default-and-the-source-changes",
  "clear-is-unavailable-when-no-override-exists",
] as const;

const requiredHeaderCaseNames = [
  "known-remote-renders-as-the-subtitle",
  "self-hosted-remote-keeps-its-full-host",
  "project-with-no-remote-renders-no-subtitle",
] as const;

const requiredProfileLinkCaseNames = [
  "profile-project-card-links-to-the-hash-keyed-project-page",
  "profile-project-card-href-encodes-a-reserved-username",
] as const;

const requiredCommonsCrumbCaseNames = [
  "the-profile-route-breadcrumb-leads-to-the-commons",
  "a-profile-that-does-not-exist-still-offers-the-way-back",
  "the-project-route-breadcrumb-leads-to-the-commons",
  "a-project-that-does-not-exist-still-offers-the-way-back",
] as const;

const commonsCrumbCaseKeys = ["name", "route", "state", "expectedLabels"];
const commonsCrumbRoutes: readonly CommonsCrumbRoute[] = ["profile", "project"];
const commonsCrumbStates: readonly CommonsCrumbState[] = ["found", "missing"];

const viewerCaseKeys = ["name", "ownerUsername", "viewerUsername", "expectRenameControl"];
const notFoundCaseKeys = ["name", "serverMessage"];
const rollupCollectiveKeys = ["id", "name", "description", "linkedGithubOrg", "transcriptCount"];
const rollupCaseKeys = [
  "name",
  "ownerUsername",
  "viewerUsername",
  "collectives",
  "expectedRowNames",
  "expectedCounts",
  "expectEmptyRollup",
];
const renameCaseKeys = [
  "name",
  "initialDisplayName",
  "initialSource",
  "action",
  "typedName",
  "expectedMethod",
  "expectedPathSuffix",
  "expectedBodyDisplayName",
  "resultingDisplayName",
  "resultingSource",
  "expectClearEnabledBefore",
];
const headerCaseKeys = ["name", "remoteLabel", "expectedSubtitle"];
const profileLinkCaseKeys = [
  "name",
  "ownerUsername",
  "projectHash",
  "projectDisplayName",
  "expectedHref",
];

const nameSources: readonly NameSource[] = ["override", "consented", "remote", "privacy"];

export function loadProjectPageFixtures(): ProjectPageFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/project-page.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("project-page fixture root must be an object");
  }
  assertExactKeys(
    parsed,
    [
      "viewerCases",
      "notFoundCases",
      "rollupCases",
      "renameCases",
      "headerCases",
      "profileLinkCases",
      "commonsCrumbCases",
    ],
    "fixture root",
  );
  const fixtures = parsed as ProjectPageFixtures;

  assertNamesMatch(fixtures.viewerCases.map((c) => c.name), requiredViewerCaseNames, "project-page viewerCases");
  for (const c of fixtures.viewerCases) {
    assertExactKeys(c, viewerCaseKeys, `viewer case ${c.name}`);
    // The control is the OWNER's. A case expecting it for anyone else would
    // silently invert the boundary it is supposed to guard.
    const viewerIsOwner =
      c.viewerUsername != null &&
      c.viewerUsername.toLowerCase() === c.ownerUsername.toLowerCase();
    if (c.expectRenameControl !== viewerIsOwner) {
      throw new Error(
        `viewer case ${c.name}: expectRenameControl is ${c.expectRenameControl} but the viewer ` +
          `${viewerIsOwner ? "IS" : "is NOT"} the owner. The correction control is owner-only, so ` +
          `these must agree; fix the expectation rather than the rule.`,
      );
    }
  }

  assertNamesMatch(
    fixtures.notFoundCases.map((c) => c.name),
    requiredNotFoundCaseNames,
    "project-page notFoundCases",
  );
  const serverMessages = new Set<string>();
  for (const c of fixtures.notFoundCases) {
    assertExactKeys(c, notFoundCaseKeys, `not-found case ${c.name}`);
    serverMessages.add(c.serverMessage);
  }
  // The corpus can only prove the rendered answer is indistinguishable if the
  // INPUTS it feeds are distinguishable. Identical server messages would make
  // the test pass no matter what the page rendered.
  if (serverMessages.size !== fixtures.notFoundCases.length) {
    throw new Error(
      `project-page notFoundCases: every case must carry a DIFFERENT serverMessage. Identical ` +
        `messages make the indistinguishability assertion vacuous, because the page would render ` +
        `the same text even if it echoed the server verbatim.`,
    );
  }

  assertNamesMatch(fixtures.rollupCases.map((c) => c.name), requiredRollupCaseNames, "project-page rollupCases");
  // An empty roll-up is claimed to be the ORDINARY answer for a viewer who is
  // not the owner. A corpus that renders every case as the owner never puts a
  // non-owner on the page, so the claim would be narrative rather than tested.
  const rollupNonOwnerCases = fixtures.rollupCases.filter(
    (c) => c.viewerUsername.toLowerCase() !== c.ownerUsername.toLowerCase(),
  );
  if (!rollupNonOwnerCases.some((c) => c.expectEmptyRollup)) {
    throw new Error(
      `project-page rollupCases: at least one case expecting an EMPTY roll-up must be viewed by ` +
        `somebody who is not the owner. Empty is described as the ordinary answer for a non-owner, ` +
        `so a corpus viewed entirely by the owner cannot show it. Set viewerUsername to a different ` +
        `user on the empty case.`,
    );
  }
  for (const c of fixtures.rollupCases) {
    assertExactKeys(c, rollupCaseKeys, `roll-up case ${c.name}`);
    for (const g of c.collectives) {
      assertExactKeys(g, rollupCollectiveKeys, `roll-up case ${c.name} collective`);
    }
    if (c.expectEmptyRollup !== (c.collectives.length === 0)) {
      throw new Error(
        `roll-up case ${c.name}: expectEmptyRollup must be true exactly when the case supplies no ` +
          `collectives`,
      );
    }
    if (
      c.expectedRowNames.length !== c.collectives.length ||
      c.expectedCounts.length !== c.collectives.length
    ) {
      throw new Error(
        `roll-up case ${c.name}: expectedRowNames and expectedCounts must each name one entry per ` +
          `supplied collective, in render order`,
      );
    }
  }

  assertNamesMatch(fixtures.renameCases.map((c) => c.name), requiredRenameCaseNames, "project-page renameCases");
  for (const c of fixtures.renameCases) {
    assertExactKeys(c, renameCaseKeys, `rename case ${c.name}`);
    for (const source of [c.initialSource, c.resultingSource]) {
      if (!nameSources.includes(source)) {
        throw new Error(
          `rename case ${c.name}: ${source} is not a NameSource. The closed set is ` +
            `${nameSources.join(", ")}.`,
        );
      }
    }
    if ((c.action === "none") !== (c.expectedMethod === null)) {
      throw new Error(
        `rename case ${c.name}: a case that performs no action must expect no request, and a case ` +
          `that acts must name the method it expects`,
      );
    }
    if (c.expectedPathSuffix != null && !c.expectedPathSuffix.includes("{hash}")) {
      throw new Error(
        `rename case ${c.name}: expectedPathSuffix must carry the {hash} placeholder. These routes ` +
          `are keyed on the project hash; a path without it would pass even if the page regressed ` +
          `to keying on a display name.`,
      );
    }
    if (c.action === "clear" && c.resultingSource === "override") {
      throw new Error(
        `rename case ${c.name}: clearing removes the override, so the resulting source can never ` +
          `still be "override"`,
      );
    }
    if (c.expectClearEnabledBefore !== (c.initialSource === "override")) {
      throw new Error(
        `rename case ${c.name}: the clear action exists only when an override does, so ` +
          `expectClearEnabledBefore must agree with initialSource being "override"`,
      );
    }
  }

  assertNamesMatch(fixtures.headerCases.map((c) => c.name), requiredHeaderCaseNames, "project-page headerCases");
  for (const c of fixtures.headerCases) {
    assertExactKeys(c, headerCaseKeys, `header case ${c.name}`);
    if (c.expectedSubtitle !== null && c.expectedSubtitle !== c.remoteLabel) {
      throw new Error(
        `header case ${c.name}: the subtitle is the server's label rendered verbatim, so ` +
          `expectedSubtitle must equal remoteLabel or be null`,
      );
    }
    if (c.remoteLabel === "" && c.expectedSubtitle !== null) {
      throw new Error(`header case ${c.name}: an absent remote label renders no subtitle`);
    }
  }

  assertNamesMatch(
    fixtures.profileLinkCases.map((c) => c.name),
    requiredProfileLinkCaseNames,
    "project-page profileLinkCases",
  );
  for (const c of fixtures.profileLinkCases) {
    assertExactKeys(c, profileLinkCaseKeys, `profile-link case ${c.name}`);
    if (!/^[0-9a-f]{64}$/.test(c.projectHash)) {
      throw new Error(
        `profile-link case ${c.name}: projectHash must be 64 lowercase hex chars, got ${c.projectHash}`,
      );
    }
    if (!c.expectedHref.endsWith(`/${c.projectHash}`)) {
      throw new Error(
        `profile-link case ${c.name}: expectedHref must end in the project hash, or the case cannot ` +
          `distinguish a hash-keyed link from a name-keyed one`,
      );
    }
  }

  assertNamesMatch(
    fixtures.commonsCrumbCases.map((c) => c.name),
    requiredCommonsCrumbCaseNames,
    "project-page commonsCrumbCases",
  );
  for (const c of fixtures.commonsCrumbCases) {
    assertExactKeys(c, commonsCrumbCaseKeys, `commons-crumb case ${c.name}`);
    if (!commonsCrumbRoutes.includes(c.route)) {
      throw new Error(
        `commons-crumb case ${c.name}: ${c.route} is not a route. The closed set is ` +
          `${commonsCrumbRoutes.join(", ")}.`,
      );
    }
    if (!commonsCrumbStates.includes(c.state)) {
      throw new Error(
        `commons-crumb case ${c.name}: ${c.state} is not a state. The closed set is ` +
          `${commonsCrumbStates.join(", ")}.`,
      );
    }
    if (c.expectedLabels.length === 0) {
      throw new Error(
        `commons-crumb case ${c.name}: a case that names no link asserts nothing. Name every ` +
          `commons-labelled link the state renders.`,
      );
    }
    for (const label of c.expectedLabels) {
      if (!/^(commons|back to commons)$/i.test(label)) {
        throw new Error(
          `commons-crumb case ${c.name}: ${label} does not read as a link to the commons, so a ` +
            `case about commons links cannot be about it`,
        );
      }
    }
  }
  // Both routes and BOTH states must appear. The not-found states carry three of
  // the five links, so a corpus covering only the ordinary render would leave
  // most of them free to regress.
  for (const route of commonsCrumbRoutes) {
    for (const state of commonsCrumbStates) {
      if (!fixtures.commonsCrumbCases.some((c) => c.route === route && c.state === state)) {
        throw new Error(
          `project-page commonsCrumbCases: the ${route} route's ${state} state has no case. Every ` +
            `state that renders a way back to the commons has to pin where it leads.`,
        );
      }
    }
  }

  return fixtures;
}
