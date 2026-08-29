import { fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installProfileRouteREST,
  installProjectRouteREST,
  installProjectRouteTeardown,
  renderProfileRoute,
  renderProjectRoute,
  type ProjectRouteFixture,
} from "@/test/mountedProjectRoute";
import { loadProjectPageFixtures } from "@/test/projectPageFixtures";
import { EXPLORE_SECTION } from "@/lib/nav/sections";

// Mounts the REAL production routes: the project page
// (`/users/{username}/projects/{projectHash}`) and the profile page whose
// project headings link into it. Every assertion is on what lands in the DOM
// for a given viewer, and on the request a control actually issues, so a
// regression cannot hide behind a prop snapshot.

const fixtures = loadProjectPageFixtures();

const HASH = "1111111111111111111111111111111111111111111111111111111111111111";

function baseFixture(overrides: Partial<ProjectRouteFixture> = {}): ProjectRouteFixture {
  return {
    viewer: "alice-dev",
    ownerUsername: "alice-dev",
    projectHash: HASH,
    displayName: "village",
    nameSource: "consented",
    remoteLabel: "github.com:peasant-labs/village",
    transcriptTitles: ["first session", "second session"],
    collectives: [],
    ...overrides,
  };
}

async function renderReady(fixture: ProjectRouteFixture): Promise<void> {
  await renderProjectRoute(fixture.ownerUsername, fixture.projectHash);
  await waitFor(() =>
    expect(
      document.querySelector('[data-testid="project-display-name"]') ??
        document.querySelector('[data-testid="project-page-not-found"]'),
    ).not.toBeNull(),
  );
}

installProjectRouteTeardown();

describe("mounted project page: the owner-only correction control", () => {
  for (const c of fixtures.viewerCases) {
    it(c.name, async () => {
      const fixture = baseFixture({
        ownerUsername: c.ownerUsername,
        viewer: c.viewerUsername,
      });
      installProjectRouteREST(fixture);
      await renderReady(fixture);

      const control = document.querySelector('[data-testid="project-rename-control"]');
      expect(control !== null).toBe(c.expectRenameControl);

      // Beyond the container: the actions themselves must be absent for a
      // viewer who is not the owner, so a regression that leaked only the
      // buttons is caught as well.
      const buttonLabels = [...document.querySelectorAll("button")].map((b) =>
        (b.textContent ?? "").trim(),
      );
      expect(buttonLabels.includes("save name")).toBe(c.expectRenameControl);
      expect(buttonLabels.includes("reset to default")).toBe(c.expectRenameControl);
    });
  }
});

describe("mounted project page: one indistinguishable not-found answer", () => {
  // Every refusal this route can make arrives as a 404 and must render the SAME
  // words. The rendered text is collected across cases and compared: if the page
  // ever echoes the server's (deliberately different) message, or grows a
  // case-specific hint, the collected set stops being a single value.
  const rendered: string[] = [];

  for (const c of fixtures.notFoundCases) {
    it(c.name, async () => {
      const fixture = baseFixture({
        errorStatus: 404,
        errorMessage: c.serverMessage,
      });
      installProjectRouteREST(fixture);
      await renderReady(fixture);

      const panel = document.querySelector('[data-testid="project-page-not-found"]');
      expect(panel).not.toBeNull();
      const text = (panel?.textContent ?? "").replace(/\s+/g, " ").trim();

      // The server's own wording must not reach the page.
      expect(text).not.toContain(c.serverMessage);
      rendered.push(text);
    });
  }

  it("renders identical wording for every refusal", () => {
    expect(rendered).toHaveLength(fixtures.notFoundCases.length);
    expect(new Set(rendered).size).toBe(1);
  });
});

describe("mounted project page: the collectives roll-up", () => {
  for (const c of fixtures.rollupCases) {
    it(c.name, async () => {
      const fixture = baseFixture({
        ownerUsername: c.ownerUsername,
        viewer: c.viewerUsername,
        collectives: c.collectives.map((g) => ({
          id: g.id,
          name: g.name,
          description: g.description,
          linked_github_org: g.linkedGithubOrg,
          transcript_count: g.transcriptCount,
        })),
      });
      installProjectRouteREST(fixture);
      await renderReady(fixture);

      const panel = document.querySelector('[data-testid="project-collectives"]');
      expect(panel).not.toBeNull();
      const panelText = (panel?.textContent ?? "").replace(/\s+/g, " ").trim();

      // The case renders as the viewer it names, so a case declared as a
      // non-owner really is one: the owner-only control is absent for them.
      const viewerIsOwner =
        c.viewerUsername.toLowerCase() === c.ownerUsername.toLowerCase();
      expect(document.querySelector('[data-testid="project-rename-control"]') !== null).toBe(
        viewerIsOwner,
      );

      const rowNames = [...panel!.querySelectorAll("li a")].map((a) =>
        (a.textContent ?? "").trim(),
      );
      expect(rowNames).toEqual(c.expectedRowNames);

      const counts = [...panel!.querySelectorAll('[data-testid="collective-transcript-count"]')].map(
        (el) => (el.textContent ?? "").trim(),
      );
      expect(counts).toEqual(c.expectedCounts);

      if (c.expectEmptyRollup) {
        // Empty is an ORDINARY answer for a viewer who is not the owner: the
        // roll-up is gated by collective visibility and the owner's contributor
        // opt-in. Rendering it as a failure, or hinting that something is being
        // withheld, would confirm the memberships the gate exists to hide.
        expect(panel!.querySelectorAll("li")).toHaveLength(0);
        expect(document.querySelector('[role="alert"]')).toBeNull();
        for (const forbidden of ["error", "failed", "hidden", "withheld", "private", "not allowed"]) {
          expect(panelText.toLowerCase()).not.toContain(forbidden);
        }
        expect(panelText.toLowerCase()).toContain("no collectives to show");
      }
    });
  }
});

describe("mounted project page: the hash-keyed set and clear", () => {
  for (const c of fixtures.renameCases) {
    it(c.name, async () => {
      const fixture = baseFixture({
        displayName: c.initialDisplayName,
        nameSource: c.initialSource,
        afterCorrection: {
          displayName: c.resultingDisplayName,
          nameSource: c.resultingSource,
        },
      });
      const requests = installProjectRouteREST(fixture);
      await renderReady(fixture);

      const control = document.querySelector('[data-testid="project-rename-control"]');
      expect(control).not.toBeNull();
      const buttons = [...control!.querySelectorAll("button")];
      const save = buttons.find((b) => (b.textContent ?? "").trim() === "save name")!;
      const clear = buttons.find((b) => (b.textContent ?? "").trim() === "reset to default")!;
      expect(save).toBeDefined();
      expect(clear).toBeDefined();
      expect(!clear.hasAttribute("disabled")).toBe(c.expectClearEnabledBefore);

      const input = control!.querySelector("input") as HTMLInputElement;
      expect(input.value).toBe(c.initialDisplayName);

      const before = requests.length;
      if (c.action === "set") {
        fireEvent.change(input, { target: { value: c.typedName } });
        await waitFor(() => expect(save.hasAttribute("disabled")).toBe(false));
        fireEvent.click(save);
      } else if (c.action === "clear") {
        fireEvent.click(clear);
      }

      if (c.expectedMethod === null) {
        // A case that performs no action must not have issued a correction.
        expect(requests.slice(before).filter((r) => r.method !== "GET")).toHaveLength(0);
        return;
      }

      await waitFor(() => {
        const issued = requests.slice(before).filter((r) => r.method === c.expectedMethod);
        expect(issued).toHaveLength(1);
      });
      const issued = requests.slice(before).find((r) => r.method === c.expectedMethod)!;
      const expectedPath = c.expectedPathSuffix!.replace("{hash}", fixture.projectHash);
      expect(issued.url.endsWith(expectedPath)).toBe(true);
      // The request is keyed on the HASH, never on a display name. A body or a
      // path carrying the name as a KEY is the defect these routes replaced.
      expect(issued.url).not.toContain(encodeURIComponent(c.initialDisplayName));
      if (c.expectedBodyDisplayName === null) {
        expect(issued.body).toBeNull();
      } else {
        expect(issued.body).toEqual({ display_name: c.expectedBodyDisplayName });
      }

      // The control re-reads the server's answer: after a clear, both the name
      // and the tier it now comes from change, so a stale echo is visible.
      await waitFor(() => {
        const heading = document.querySelector('[data-testid="project-display-name"]');
        expect((heading?.textContent ?? "").trim()).toBe(c.resultingDisplayName);
      });
      await waitFor(() => {
        const nowInput = document.querySelector(
          '[data-testid="project-rename-control"] input',
        ) as HTMLInputElement;
        expect(nowInput.value).toBe(c.resultingDisplayName);
      });
      const nowControl = document.querySelector('[data-testid="project-rename-control"]')!;
      const nowClear = [...nowControl.querySelectorAll("button")].find(
        (b) => (b.textContent ?? "").trim() === "reset to default",
      )!;
      expect(!nowClear.hasAttribute("disabled")).toBe(c.resultingSource === "override");
    });
  }
});

describe("mounted project page: the header and its one subtitle", () => {
  for (const c of fixtures.headerCases) {
    it(c.name, async () => {
      const fixture = baseFixture({ remoteLabel: c.remoteLabel });
      installProjectRouteREST(fixture);
      await renderReady(fixture);

      const heading = document.querySelector('[data-testid="project-display-name"]');
      // The display name is USER CONTENT and is rendered exactly as stored.
      expect((heading?.textContent ?? "").trim()).toBe(fixture.displayName);

      const subtitle = document.querySelector('[data-testid="project-remote-label"]');
      if (c.expectedSubtitle === null) {
        expect(subtitle).toBeNull();
      } else {
        expect((subtitle?.textContent ?? "").trim()).toBe(c.expectedSubtitle);
      }
    });
  }
});

describe("mounted profile page: project cards link into the project page", () => {
  for (const c of fixtures.profileLinkCases) {
    it(c.name, async () => {
      installProfileRouteREST(c.ownerUsername, c.ownerUsername, [
        { projectHash: c.projectHash, projectDisplayName: c.projectDisplayName },
      ]);
      await renderProfileRoute(c.ownerUsername);
      await waitFor(() =>
        expect(
          [...document.querySelectorAll("a")].some(
            (a) => (a.textContent ?? "").trim() === c.projectDisplayName,
          ),
        ).toBe(true),
      );

      // Assert the navigation a user actually triggers: the anchor carrying the
      // project's name must target the hash-keyed route.
      const anchor = [...document.querySelectorAll("a")].find(
        (a) => (a.textContent ?? "").trim() === c.projectDisplayName,
      )!;
      expect(anchor.getAttribute("href")).toBe(c.expectedHref);

      // The old inline rename flow is gone from this surface: the project's name
      // is now the way IN to the page that owns the correction control.
      const labels = [...document.querySelectorAll("button")].map((b) =>
        (b.textContent ?? "").trim().toLowerCase(),
      );
      expect(labels).not.toContain("rename");
    });
  }
});

describe("mounted routes: every commons link leads to the commons", () => {
  // The root of the app serves the signed-in person their own home page, so a
  // link labelled for the commons can no longer point at "/": for the very
  // people who see these pages most, that address is their home, not
  // discovery. The label and the destination only agree if both come from the
  // one exported constant. The not-found states carry most of these links, so
  // they are exercised too.
  for (const c of fixtures.commonsCrumbCases) {
    it(c.name, async () => {
      const missing = c.state === "missing";
      if (c.route === "profile") {
        installProfileRouteREST(
          "alice-dev",
          // A viewer looking at somebody else's profile: the route shows its
          // own not-found state only to someone who does not own it.
          missing ? "someone-else" : "alice-dev",
          [{ projectHash: HASH, projectDisplayName: "village" }],
          missing ? 404 : undefined,
        );
        await renderProfileRoute("alice-dev");
      } else {
        installProjectRouteREST(
          baseFixture(missing ? { errorStatus: 404, errorMessage: "no such project" } : {}),
        );
        await renderProjectRoute("alice-dev", HASH);
      }

      const links = await waitFor(() => {
        const found = [...document.querySelectorAll("a")].filter((a) =>
          /^(commons|back to commons)$/i.test((a.textContent ?? "").trim()),
        );
        expect(found.map((a) => (a.textContent ?? "").trim())).toEqual(c.expectedLabels);
        return found;
      });

      for (const link of links) {
        expect(link.getAttribute("href")).toBe(EXPLORE_SECTION.href);
        // Not the bare root: that is somebody's home page now.
        expect(link.getAttribute("href")).not.toBe("/");
      }
    });
  }
});
