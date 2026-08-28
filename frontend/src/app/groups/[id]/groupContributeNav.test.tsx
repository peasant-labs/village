import { describe, expect, it, vi, type Mock } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { Manage } from "@peasant-labs/fairtrade/commons";
import {
  installGroupRouteREST,
  installGroupRouteTeardown,
  loadGroupsContributeNavFixtures,
  renderGroupContributeRoute,
  renderGroupDetailRoute,
} from "@/test/mountedGroupRoute";

/**
 * Mounts the REAL `/groups/{id}` and `/groups/{id}/contribute` routes to
 * prove: (1) the header contribute action on `/groups/{id}` navigates a
 * member to the dedicated route instead of toggling the retired inline
 * panel, and never reaches a non-member as a clickable control; (2) the
 * dedicated route itself renders the moved selection panel for a member and
 * a membership notice for everyone else. `@peasant-labs/fairtrade/commons`'s
 * `Manage` is stubbed to capture the `actions` object `/groups/{id}` passes
 * it — the same idiom `sessionPageOrchestration.test.tsx` uses for `Explore`
 * — so the assertion is on the real page's wiring, not a hand-built prop.
 */
interface CapturedManageActions {
  onContribute?: () => void;
}

const h = vi.hoisted(() => {
  return {
    push: vi.fn(),
    manageActions: { current: null } as { current: CapturedManageActions | null },
  };
});

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: h.push }),
}));

vi.mock("@peasant-labs/fairtrade/commons", () => ({
  Manage: vi.fn((props: { actions: CapturedManageActions }) => {
    h.manageActions.current = props.actions;
    return null;
  }),
}));

installGroupRouteTeardown();

// Read through a function, never a bare `h.manageActions.current` expression:
// TS's control-flow narrowing of a dotted property across `await` points is
// unreliable here (it can collapse the union to `never`) once the same
// property is both reset to `null` in a test and reassigned inside the
// hoisted mock's closure. A function call boundary sidesteps that narrowing
// entirely.
function currentManageActions(): CapturedManageActions | null {
  return h.manageActions.current;
}

// `Manage`'s real (fairtrade) type signature doesn't match the narrow shape
// this test's mock factory declares, so `vi.mocked(Manage)` fights the type
// checker over a mock that is, at runtime, exactly the `vi.fn` from the
// factory above. Read it through this one local cast instead of scattering
// `as unknown as Mock` at every call site.
const manageMock = Manage as unknown as Mock<
  (props: { actions: CapturedManageActions }) => ReactElement | null
>;

// The default stub every test but `owner_no_double_button_...` relies on:
// capture the actions object, render nothing. Kept as a named function so the
// double-button test can restore it after installing a persistent override
// (see that test for why the override must be persistent, not `Once`).
function defaultManageImpl(props: { actions: CapturedManageActions }): ReactElement | null {
  h.manageActions.current = props.actions;
  return null;
}

// Loading the fixture here (module scope) makes a renamed/deleted/added row
// fail every test in this file immediately -- the required-name manifest
// check inside loadGroupsContributeNavFixtures is the mutation guard.
const fixtures = loadGroupsContributeNavFixtures();
const rowNames = new Set(fixtures.rows.map((r) => r.name));

describe("groups contribute navigation", () => {
  it("member_navigates: header contribute action navigates to the dedicated route", async () => {
    expect(rowNames.has("member_navigates")).toBe(true);
    h.push.mockClear();
    h.manageActions.current = null;
    installGroupRouteREST({
      viewer: "alice",
      groupId: "grp-nav-1",
      groupName: "acme collective",
      role: "member",
    });

    await renderGroupDetailRoute("grp-nav-1");
    await waitFor(() => expect(currentManageActions()).not.toBeNull());

    const onContribute = currentManageActions()?.onContribute;
    expect(onContribute).toBeDefined();
    onContribute!();

    expect(h.push).toHaveBeenCalledWith("/groups/grp-nav-1/contribute");
    expect(h.push).toHaveBeenCalledTimes(1);
    // The retired inline panel is gone -- no panel markup mounts alongside Manage.
    expect(screen.queryByTestId("contribute-member-panel")).not.toBeInTheDocument();
    // A member takes the manage surface's own action, so village must NOT
    // render a second contribute button beside it.
    expect(screen.queryByRole("button", { name: "contribute" })).not.toBeInTheDocument();
  });

  it("owner_navigates_via_village_action: an owner reaches the route through village's own action", async () => {
    expect(rowNames.has("owner_navigates_via_village_action")).toBe(true);
    h.push.mockClear();
    h.manageActions.current = null;
    installGroupRouteREST({
      viewer: "olivia",
      groupId: "grp-nav-5",
      groupName: "acme collective",
      role: "owner",
    });

    await renderGroupDetailRoute("grp-nav-5");
    await waitFor(() => expect(currentManageActions()).not.toBeNull());

    // The shared manage surface is stubbed to render nothing, so the control
    // found here can only be the one village renders itself.
    const action = await screen.findByRole("button", { name: "contribute" });
    act(() => {
      fireEvent.click(action);
    });

    expect(h.push).toHaveBeenCalledWith("/groups/grp-nav-5/contribute");
    expect(h.push).toHaveBeenCalledTimes(1);
  });

  it("owner_no_double_button_even_if_manage_renders_its_own: an owner never gets two contribute controls", async () => {
    expect(rowNames.has("owner_no_double_button_even_if_manage_renders_its_own")).toBe(true);
    h.push.mockClear();
    h.manageActions.current = null;
    installGroupRouteREST({
      viewer: "olivia",
      groupId: "grp-nav-6",
      groupName: "acme collective",
      role: "owner",
    });

    // Simulate a Manage surface that renders whatever contribute action it
    // is handed, instead of gating on its own internal role check. This
    // proves the no-double-button guarantee lives at the village boundary
    // (onContribute withheld for an owner in groups/[id]/page.tsx) rather
    // than depending on Manage's own internal role gate never firing for an
    // owner. Mutation: widening the gate back to `isMember` (owner
    // included) makes Manage's stub render a second "contribute" button and
    // this test goes red.
    //
    // Persistent, not `Once`: a one-shot override only covers the FIRST
    // render. React Query's fetch-then-settle flow re-renders this route
    // after data loads, so a `mockImplementationOnce` stub falls back to
    // `defaultManageImpl` (which renders no button at all) on that second
    // render -- the assertion below would then pass vacuously against zero
    // buttons instead of proving exactly one survives a widened gate.
    manageMock.mockImplementation((props) => {
      h.manageActions.current = props.actions;
      if (!props.actions.onContribute) return null;
      return <button onClick={props.actions.onContribute}>contribute</button>;
    });

    try {
      await renderGroupDetailRoute("grp-nav-6");
      await waitFor(() => expect(currentManageActions()).not.toBeNull());

      expect(screen.getAllByRole("button", { name: "contribute" })).toHaveLength(1);
    } finally {
      // A persistent override must not leak into later tests in this file.
      manageMock.mockImplementation(defaultManageImpl);
    }
  });

  it("non_member_no_button: no onContribute action reaches Manage for a non-member", async () => {
    expect(rowNames.has("non_member_no_button")).toBe(true);
    h.push.mockClear();
    h.manageActions.current = null;
    installGroupRouteREST({
      viewer: "bob",
      groupId: "grp-nav-2",
      groupName: "acme collective",
      role: null,
    });

    await renderGroupDetailRoute("grp-nav-2");
    await waitFor(() => expect(currentManageActions()).not.toBeNull());

    expect(currentManageActions()?.onContribute).toBeUndefined();
  });

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
