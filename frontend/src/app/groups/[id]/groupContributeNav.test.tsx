import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import {
  installGroupRouteREST,
  installGroupRouteTeardown,
  loadGroupsContributeNavFixtures,
  renderGroupContributeRoute,
} from "@/test/mountedGroupRoute";

/**
 * Mounts the REAL `/groups/{id}/contribute` route to prove the dedicated route
 * renders the selection panel for a member and a membership notice for
 * everyone else. How the collective page reaches this route and the review
 * route is covered by the overflow cases in `src/testdata/collective-page.yaml`.
 */
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

installGroupRouteTeardown();

// Loading the fixture here (module scope) makes a renamed/deleted/added row
// fail every test in this file immediately -- the required-name manifest
// check inside loadGroupsContributeNavFixtures is the mutation guard.
const fixtures = loadGroupsContributeNavFixtures();
const rowNames = new Set(fixtures.rows.map((r) => r.name));

describe("groups contribute navigation", () => {
  it("contribute_page_member_panel: a member sees the moved selection panel", async () => {
    expect(rowNames.has("contribute_page_member_panel")).toBe(true);
    installGroupRouteREST({
      viewer: "alice",
      groupId: "grp-nav-3",
      groupName: "acme collective",
      role: "member",
    });

    await renderGroupContributeRoute("grp-nav-3");

    expect(await screen.findByTestId("contribute-member-panel")).toBeInTheDocument();
    expect(screen.getByText("contribute to acme collective")).toBeInTheDocument();
    expect(screen.queryByTestId("contribute-non-member-notice")).not.toBeInTheDocument();
  });

  it("contribute_page_non_member_notice: a non-member sees a notice and a back link", async () => {
    expect(rowNames.has("contribute_page_non_member_notice")).toBe(true);
    installGroupRouteREST({
      viewer: "bob",
      groupId: "grp-nav-4",
      groupName: "acme collective",
      role: null,
    });

    await renderGroupContributeRoute("grp-nav-4");

    expect(await screen.findByTestId("contribute-non-member-notice")).toBeInTheDocument();
    expect(screen.getByText(/back to acme collective/)).toBeInTheDocument();
    expect(screen.queryByTestId("contribute-member-panel")).not.toBeInTheDocument();
  });
});
