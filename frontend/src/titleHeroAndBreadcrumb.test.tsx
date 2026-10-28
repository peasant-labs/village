import { waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import {
  loadTitleHeroAndBreadcrumbFixtures,
  type TitleHeroAndBreadcrumbCase,
} from "@/test/titleHeroAndBreadcrumbFixtures";

// Mounts the REAL production route (TranscriptDetailPage -> SessionDetailV2 ->
// fairtrade's TranscriptViewer) with REST mocked, following the same pattern
// as mountedObservedModelRoute.test.tsx. Covers village#32 (the detail hero
// deriving its own title instead of showing the stored one) and village#33
// (the breadcrumb project crumb now links to the project page instead of
// being a dead label).
//
// Each fixture case renders TWICE, in two independent `it` blocks below, so
// the two production defects it guards against are independently
// observable: reverting only the title overlay must fail only the "hero
// title" tests, and reverting only the project-crumb href must fail only the
// "breadcrumb" tests. A single combined `it` per case would still catch both
// regressions, but could not say WHICH one broke from its own output alone.
//
// Every case here goes through installRESTFixture's project-identity
// defaults (see mountedProductionRoute.tsx): project_display_name defaults
// to project_name and owner.github_username defaults to "fixture-owner", so
// the expected project href below is always
// `/users/fixture-owner/projects/{DEFAULT_PROJECT_HASH}`.
const DEFAULT_PROJECT_HASH = "0".repeat(64);
const DEFAULT_OWNER_USERNAME = "fixture-owner";
const expectedProjectHref = `/users/${DEFAULT_OWNER_USERNAME}/projects/${DEFAULT_PROJECT_HASH}`;

const fixtures = loadTitleHeroAndBreadcrumbFixtures();

function buildSessionDetail(c: TitleHeroAndBreadcrumbCase): SessionDetailPayload {
  const userTurn: NonNullable<SessionDetailPayload["turns"]>[number] = {
    index: 0,
    role: "user",
    content: c.firstUserTurnContent,
    timestamp: "2026-08-21T09:00:00.000Z",
    depth: 0,
  };
  const assistantTurn: NonNullable<SessionDetailPayload["turns"]>[number] = {
    index: 1,
    role: "assistant",
    content: "Understood — investigating now.",
    timestamp: "2026-08-21T09:01:00.000Z",
    depth: 0,
  };
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
    project: c.project,
    model: "anthropic/claude-fable-5",
    turns: [userTurn, assistantTurn],
  };
}

async function renderCase(
  c: TitleHeroAndBreadcrumbCase,
  options: { signedIn?: boolean } = {},
): Promise<{ transcriptID: string }> {
  const transcriptID = `transcript-${c.name}`;
  const detail = buildSessionDetail(c);
  const metadata: MountedRouteTranscriptMetadata = {
    transcript: {
      id: transcriptID,
      local_id: detail.id,
      visibility: "public",
      title: c.storedTitle,
      description: null,
      project_name: c.project,
    },
    owner: { id: "fixture-owner" },
    enriched_shares: [],
  };
  installRESTFixture(
    transcriptID,
    metadata,
    detail,
    "title-hero-and-breadcrumb",
    options.signedIn ? { id: "fixture-reader", github_username: "fixture-reader", orgs: [] } : undefined,
  );
  await renderProductionRoute(transcriptID, "", { signedIn: options.signedIn });
  await waitFor(() => expect(document.querySelector(".txn-title")).not.toBeNull());
  return { transcriptID };
}

installMountedRouteTeardown();

describe("mounted production transcript route: stored title hero", () => {
  for (const c of fixtures.cases) {
    // Independent of every breadcrumb assertion below — reverting only the
    // title overlay must fail only this test.
    it(c.name, async () => {
      await renderCase(c);
      const hero = document.querySelector(".txn-title");
      expect(hero?.textContent).toBe(c.expectedHeroTitle);
      // The transcript is the whole page, so its title is the page's one h1.
      expect(hero?.tagName).toBe("H1");
      expect(document.querySelectorAll("h1")).toHaveLength(1);
    });
  }
});

describe("mounted production transcript route: routable breadcrumb", () => {
  for (const c of fixtures.cases) {
    // Independent of the title assertion above — reverting only the
    // hardcoded /projects hrefs must fail only this test.
    it(c.name, async () => {
      // Signed in, so the trail's crumbs are links (signed out they are plain
      // text: `transcript-context.yaml`'s signed-out cases).
      await renderCase(c, { signedIn: true });

      // Every href must resolve to a real village route, and specifically
      // none may start with the removed, always-404ing /projects prefix
      // (village#33) — the real project route is under /users/{username}.
      const nav = document.querySelector('nav[aria-label="breadcrumb"]');
      expect(nav).not.toBeNull();

      const allHrefs = [...document.querySelectorAll("a")].map((a) => a.getAttribute("href"));
      for (const href of allHrefs) {
        expect(href?.startsWith("/projects")).toBe(false);
      }

      // These fixtures open the transcript with no context, in no
      // collective: the trail is home, project, then the current title.
      const crumbs = [...nav!.querySelectorAll(".link, .cur")].map((crumb) => crumb.textContent);
      expect(crumbs).toEqual(["home", c.expectedProjectCrumbLabel, c.expectedHeroTitle]);
      // The project remains a real exit to its page.
      const crumbAnchors = [...nav!.querySelectorAll("a")];
      expect(crumbAnchors.map((a) => a.getAttribute("href"))).toEqual(["/", expectedProjectHref]);
      expect(crumbAnchors[1]?.textContent).toBe(c.expectedProjectCrumbLabel);

    });
  }
});
