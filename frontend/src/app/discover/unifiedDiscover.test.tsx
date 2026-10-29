import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  installSurfacesREST,
  installSurfacesTeardown,
  readFixture,
  renderDiscoverPage,
  worldFrom,
  type WorldCollective,
} from "@/test/mountedCollectivesSurfaces";
import { openExploreSearch } from "@/lib/exploreSearch";
import { replacedRoutes } from "@/test/nextNavigationMock";

const fixture = readFixture("discover.yaml");
const navigation = fixture.navigation as {
  path: string;
  transcriptQuery: string;
  collectiveQuery: string;
  transcriptPath: string;
  collectivePath: string;
};

installSurfacesTeardown();
afterEach(() => window.history.replaceState(null, "", "/"));

it("keeps both discovery searches reachable, retains filters, and focuses the visible search", async () => {
  window.history.replaceState(null, "", navigation.path);
  installSurfacesREST(worldFrom({ visible: fixture.visible as WorldCollective[], searchable: fixture.searchable as WorldCollective[] }));
  const collectivesFetch = globalThis.fetch;
  // Discovery's transcript body uses the real query and adapter, with an empty
  // valid response. The existing fixtures cover pagination and result behavior.
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input));
    if (url.pathname.endsWith("/tags/popular")) return Response.json([]);
    if (url.pathname.endsWith("/transcripts")) {
      const page = Number(url.searchParams.get("page") ?? 1);
      const limit = Number(url.searchParams.get("limit") ?? 20);
      return Response.json(url.searchParams.get("view") === "grouped"
        ? { items: [], page, limit, totalItems: 0, ordinarySessionTotal: 0, helperThreadTotal: 0 }
        : { transcripts: [], page, limit, total: 0, agent_total: 0 });
    }
    return collectivesFetch(input, init);
  });
  await renderDiscoverPage();
  const tabs = screen.getByRole("tablist", { name: "discover" });
  const collectiveTab = within(tabs).getByRole("tab", { name: "collectives" });
  const transcriptTab = within(tabs).getByRole("tab", { name: "transcripts" });
  expect(collectiveTab).toHaveAttribute("aria-selected", "true");
  const collectiveSearch = screen.getByRole("searchbox", { name: "find a collective" });
  fireEvent.change(collectiveSearch, { target: { value: navigation.collectiveQuery } });

  fireEvent.click(transcriptTab);
  const transcriptSearch = await screen.findByRole("textbox", { name: /^search transcripts/ });
  fireEvent.change(transcriptSearch, { target: { value: navigation.transcriptQuery } });
  expect(replacedRoutes).toContain(navigation.transcriptPath);
  act(() => openExploreSearch(vi.fn(), navigation.path));
  expect(transcriptSearch).toHaveFocus();

  fireEvent.click(collectiveTab);
  expect(collectiveSearch).toHaveValue(navigation.collectiveQuery);
  expect(replacedRoutes).toContain(navigation.collectivePath);
  act(() => openExploreSearch(vi.fn(), navigation.path));
  expect(collectiveSearch).toHaveFocus();

  fireEvent.click(transcriptTab);
  await waitFor(() => expect(screen.getByRole("textbox", { name: /^search transcripts/ })).toHaveValue(navigation.transcriptQuery));
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
});
