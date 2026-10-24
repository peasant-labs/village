import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import type { PublicOwner } from "@/lib/publicOwner";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
} from "@/test/mountedProductionRoute";

type AnonymousOwnerCase = {
  name: string;
  owner: PublicOwner;
  project: string | null;
  contentProject: string | null;
  expectedCrumbs: string[];
  expectedUserLinks: string[];
};

const requiredNames = [
  "profile-owner-shows-the-project-crumb-and-link",
  "anonymous-owner-shows-no-project-and-no-link",
] as const;

function loadCases(): AnonymousOwnerCase[] {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/anonymous-owner-transcript.yaml"), "utf8"), {
    strict: true,
  }) as { cases: AnonymousOwnerCase[] };
  assertExactKeys(parsed, ["cases"], "anonymous-owner-transcript fixture root");
  assertNamesMatch(parsed.cases.map((c) => c.name), requiredNames, "anonymous-owner-transcript cases");
  for (const c of parsed.cases) {
    assertExactKeys(c, ["name", "owner", "project", "contentProject", "expectedCrumbs", "expectedUserLinks"], `case ${c.name}`);
  }
  return parsed.cases;
}

/** The content read, with the repository fields left out when withheld. */
function sessionDetail(c: AnonymousOwnerCase): SessionDetailPayload {
  return {
    id: `session-${c.name}`,
    harness: "claude-code",
    startTime: "2026-08-21T09:00:00.000Z",
    endTime: "2026-08-21T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 200,
    tokensIn: 120,
    tokensOut: 80,
    turnCount: 2,
    toolCallCount: 0,
    ...(c.contentProject == null
      ? {}
      : { project: c.contentProject, workingDirectory: `/work/${c.contentProject}`, gitBranch: "main", gitRemote: "git@github.com:acme/village.git" }),
    model: "anthropic/claude-fable-5",
    turns: [
      { index: 0, role: "user", content: "check the ingest path", timestamp: "2026-08-21T09:00:00.000Z", depth: 0 },
      { index: 1, role: "assistant", content: "Looking now.", timestamp: "2026-08-21T09:01:00.000Z", depth: 0 },
    ],
  };
}

installMountedRouteTeardown();

describe("the transcript page draws only the owner and project it receives", () => {
  for (const c of loadCases()) {
    it(c.name, async () => {
      const transcriptID = `transcript-${c.name}`;
      installRESTFixture(
        transcriptID,
        {
          transcript: {
            id: transcriptID,
            local_id: `session-${c.name}`,
            visibility: "public",
            title: "a public session",
            description: null,
            project_name: c.project,
          },
          owner: c.owner,
          enriched_shares: [],
        },
        sessionDetail(c),
        "anonymous-owner-transcript",
      );
      await renderProductionRoute(transcriptID);
      await waitFor(() => expect(document.querySelector(".txn-title")).not.toBeNull());

      const nav = document.querySelector('nav[aria-label="breadcrumb"]');
      expect(nav).not.toBeNull();
      expect([...nav!.querySelectorAll(".link, .cur")].map((crumb) => crumb.textContent)).toEqual(c.expectedCrumbs);
      const userLinks = [...document.querySelectorAll("a")]
        .map((a) => a.getAttribute("href") ?? "")
        .filter((href) => href.startsWith("/users/"));
      expect(userLinks).toEqual(c.expectedUserLinks);
      if (c.project === null) {
        // Nothing from a withheld repository reaches the page.
        expect(document.body.textContent).not.toContain("village");
      }
    });
  }
});
