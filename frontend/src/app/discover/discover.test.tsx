import { describe, expect, it } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import {
  assertCaseNames,
  cardNamed,
  cards,
  installSurfacesREST,
  installSurfacesTeardown,
  readFixture,
  renderDiscoverPage,
  textOf,
  worldFrom,
  type CardRead,
  type WorldCollective,
  type WorldRefusal,
} from "@/test/mountedCollectivesSurfaces";

/**
 * The real `/discover` route, driven by `src/testdata/discover.yaml`: the
 * collectives a person can join, a search, joining from a card, and the
 * signed-out and empty states. Only HTTP is stubbed.
 */

type ExpectedCard = Omit<CardRead, "message">;

interface ListCase {
  name: string;
  why: string;
  query: string;
  expect: { request?: string; cards?: ExpectedCard[]; noMatch?: boolean };
}

interface JoinCase {
  name: string;
  why: string;
  card: string;
  discoverable: boolean;
  expect: { request: string; consent: boolean; tags: string[]; action: CardRead["action"]; message: string };
}

const fixture = readFixture("discover.yaml");
const visible = fixture.visible as WorldCollective[];
const base = {
  contributed: fixture.contributed as string[],
  searchable: fixture.searchable as Array<string | WorldCollective>,
  refusals: fixture.refusals as WorldRefusal[],
};
const freshVisible = () => visible.map((row) => ({ ...row }));
const listCases = fixture.listCases as ListCase[];
const joinCases = fixture.joinCases as JoinCase[];
assertCaseNames("discover.yaml listCases", listCases.map((c) => c.name), [
  "an-empty-search-lists-the-collectives-you-have-not-joined",
  "a-search-shows-what-the-server-matched",
  "a-search-with-no-match-says-so",
]);
assertCaseNames("discover.yaml joinCases", joinCases.map((c) => c.name), [
  "joining-an-open-collective-uses-the-join-route",
  "a-refused-join-shows-the-server-reason",
  "a-person-who-is-not-discoverable-consents-before-joining",
]);

installSurfacesTeardown();

const withoutMessage = (card: CardRead): ExpectedCard => {
  const { message: _message, ...rest } = card;
  void _message;
  return rest;
};

/** Types a query and presses Enter, which searches without waiting for typing to settle. */
function search(query: string) {
  const box = screen.getByRole("searchbox", { name: "find a collective" });
  fireEvent.change(box, { target: { value: query } });
  fireEvent.keyDown(box, { key: "Enter" });
}

describe("the discover page lists", () => {
  it.each(listCases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const requests = installSurfacesREST(worldFrom({ ...base, visible: freshVisible() }));
    await renderDiscoverPage();
    await screen.findByRole("list", { name: "collectives you can join" });

    if (c.query) search(c.query);

    if (c.expect.cards) {
      const listName = c.query ? `collectives matching ${c.query}` : "collectives you can join";
      await screen.findByRole("list", { name: listName });
      await waitFor(() => expect(cards().map(withoutMessage), c.why).toEqual(c.expect.cards));
    }
    if (c.expect.noMatch) {
      const empty = await screen.findByTestId("discover-no-match");
      expect(within(empty).getByRole("heading", { name: "no collective matches" })).toBeInTheDocument();
      fireEvent.click(within(empty).getByRole("button", { name: "clear search" }));
      await screen.findByRole("list", { name: "collectives you can join" });
      expect(screen.getByRole("searchbox", { name: "find a collective" })).toHaveValue("");
    }
    if (c.expect.request) {
      expect(requests.map((r) => r.path)).toContain(c.expect.request);
    }
  });
});

describe("joining from discover", () => {
  it.each(joinCases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const world = worldFrom({ ...base, visible: freshVisible(), viewer: { id: "user-viewer", username: "alice-dev", discoverable: c.discoverable } });
    const requests = installSurfacesREST(world);
    await renderDiscoverPage();
    await screen.findByRole("list", { name: "collectives you can join" });

    fireEvent.click(within(cardNamed(c.card)).getByRole("button", { name: `join ${c.card}` }));

    if (c.expect.consent) {
      const consent = await screen.findByRole("dialog");
      // Nothing is sent before the person consents.
      expect(requests.some((r) => r.method === "POST")).toBe(false);
      fireEvent.click(within(consent).getByRole("checkbox"));
      fireEvent.click(within(consent).getByRole("button", { name: /join/ }));
    }

    await waitFor(() => expect(requests.filter((r) => r.method === "POST").map((r) => r.path)).toEqual([c.expect.request]));
    await waitFor(() => {
      const card = cards().find((read) => read.name === c.card);
      expect(card, c.why).toMatchObject({ tags: c.expect.tags, action: c.expect.action, message: c.expect.message });
    });
    if (c.expect.message) {
      expect(within(cardNamed(c.card)).getByRole("alert")).toHaveTextContent(c.expect.message);
    }
  });

  it("offers no join on an invite-only collective", async () => {
    installSurfacesREST(worldFrom({ ...base, visible: freshVisible() }));
    await renderDiscoverPage();
    await screen.findByRole("list", { name: "collectives you can join" });
    const curated = visible.find((row) => row.mode === "curated" && row.role === null);
    if (!curated) throw new Error("discover.yaml must hold a curated collective the viewer has not joined");
    const card = cardNamed(curated.name);
    expect(within(card).queryByRole("button", { name: /join/ })).not.toBeInTheDocument();
    expect(within(card).getByRole("link", { name: `view ${curated.name}` })).toHaveAttribute("href", `/collectives/${curated.id}`);
  });
});

describe("the discover page's other states", () => {
  it("says there is nothing to discover when the person belongs to everything they can see", async () => {
    installSurfacesREST(worldFrom({ ...base, visible: freshVisible().filter((row) => row.role !== null) }));
    await renderDiscoverPage();
    const empty = await screen.findByTestId("discover-empty");
    expect(within(empty).getByRole("button", { name: "new collective" })).toBeInTheDocument();
    expect(within(empty).getByRole("link", { name: "your collectives" })).toHaveAttribute("href", "/collectives");
  });

  it("reports a failed read and reads again on retry", async () => {
    const world = worldFrom({
      ...base,
      visible: freshVisible(),
      refusals: [{ method: "GET", path: "/collectives/visible", status: 503, error: "the collectives service is unavailable" }],
    });
    const requests = installSurfacesREST(world);
    await renderDiscoverPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("the collectives service is unavailable");
    world.refusals = [];
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "try again" }));
    });
    await screen.findByRole("list", { name: "collectives you can join" });
    expect(requests.filter((r) => r.path === "/collectives/visible").length).toBeGreaterThan(1);
  });

  it("offers a signed-out visitor the sign-in page, coming back here", async () => {
    installSurfacesREST(worldFrom({ ...base, signedIn: false, visible: freshVisible() }));
    await renderDiscoverPage();
    const signedOut = await screen.findByTestId("discover-signed-out");
    expect(within(signedOut).getByRole("link", { name: "sign in" })).toHaveAttribute("href", "/login?next=%2Fdiscover");
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  });

  it("writes its chrome in lowercase", async () => {
    installSurfacesREST(worldFrom({ ...base, visible: freshVisible() }));
    await renderDiscoverPage();
    await screen.findByRole("list", { name: "collectives you can join" });
    const box = screen.getByRole("searchbox", { name: "find a collective" });
    // The search box says what it finds; the heading stands alone, with no
    // sentence explaining the page beside it.
    expect(box.getAttribute("placeholder")).toBe("collective name or github org");
    const heading = screen.getByRole("heading", { level: 1, name: "discover" });
    expect(heading.parentElement!.querySelectorAll("p")).toHaveLength(0);
    for (const card of cards()) {
      const chrome = [card.members, card.who, card.transcripts, card.action?.label ?? "", ...card.tags];
      expect(chrome.join(" "), textOf(cardNamed(card.name))).toBe(chrome.join(" ").toLowerCase());
    }
  });
});
