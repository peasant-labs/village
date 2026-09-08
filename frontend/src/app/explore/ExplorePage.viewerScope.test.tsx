import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import ExplorePage from "./ExplorePage";
import RootPage from "../page";
import { AuthProvider, useAuth } from "@/providers/AuthProvider";
import * as adapter from "@/lib/adapters/explore";
import { fixtureUser, loadExploreViewerFixtures, viewerResponse, type Viewer } from "@/test/exploreViewerFixtures";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
const fixtures = loadExploreViewerFixtures();
const clients: QueryClient[] = [];
afterEach(() => { cleanup(); clients.splice(0).forEach((c) => c.clear()); vi.restoreAllMocks(); vi.unstubAllGlobals(); document.cookie = "peasant_token=; path=/; max-age=0"; });
function json(body: unknown, status = 200) { return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } }); }
function deferred() {
  let resolve!: (value: Response) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<Response>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function AuthConsumer() {
  const auth = useAuth();
  return <output data-testid="auth-context">{JSON.stringify({ user: auth.user?.id ?? null, loading: auth.isLoading, loggedIn: auth.isLoggedIn, error: auth.isError })}</output>;
}
function mount(route = "explore") {
  const authRequests: ReturnType<typeof deferred>[] = [];
  const listRequests: (ReturnType<typeof deferred> & { url: URL; authorization: string | null })[] = [];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  clients.push(client);
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input));
    if (url.pathname.endsWith("/auth/me")) { const request = deferred(); authRequests.push(request); return request.promise; }
    if (url.pathname.endsWith("/transcripts")) {
      const request = { ...deferred(), url, authorization: new Headers(init?.headers).get("Authorization") };
      listRequests.push(request); return request.promise;
    }
    if (url.pathname.endsWith("/groups/search")) return Promise.resolve(json({ collectives: [] }));
    if (url.pathname.endsWith("/tags/popular")) return Promise.resolve(json([]));
    throw new Error(`unexpected fixture request ${url.pathname}`);
  }));
  // Call-through observation of the production adapter, never a substitute component.
  const adapted = vi.spyOn(adapter, "adaptExplore");
  render(<QueryClientProvider client={client}><AuthProvider><AuthConsumer />{route === "home" ? <RootPage /> : <ExplorePage />}</AuthProvider></QueryClientProvider>);
  function cache(viewer: Viewer) { return client.getQueriesData({ queryKey: ["transcripts", viewer] }).map(([, data]) => data).filter((data) => data !== undefined); }
  async function auth(index: number, viewer: Viewer) {
    await waitFor(() => expect(authRequests[index]).toBeDefined());
    await act(async () => authRequests[index].resolve(viewer === "anonymous" ? json({ error: "expired" }, 401) : json(fixtureUser(viewer))));
  }
  async function list(index: number, viewer: Viewer) {
    await waitFor(() => expect(listRequests[index]).toBeDefined());
    const page = Number(listRequests[index].url.searchParams.get("page"));
    const body = viewerResponse(fixtures.viewers[viewer], viewer, page);
    await act(async () => listRequests[index].resolve(json(body)));
    await waitFor(() => expect(document.querySelector(`a[href="/transcripts/${fixtures.viewers[viewer].row}"]`)).not.toBeNull());
    expect(cache(viewer)).toContainEqual(body);
    const output = adapted.mock.results.at(-1)?.value;
    expect(output.harnessFacets).toEqual(body.harness_facets);
    expect(output.transcripts.transcripts.map((row: { id: string }) => row.id)).toEqual([fixtures.viewers[viewer].row]);
    return body;
  }
  function hidden(viewer: Viewer) { expect(document.querySelector(`a[href="/transcripts/${fixtures.viewers[viewer].row}"]`)).toBeNull(); }
  function visible(viewer: Viewer) { expect(document.querySelector(`a[href="/transcripts/${fixtures.viewers[viewer].row}"]`)).not.toBeNull(); }
  function beginAuth() { act(() => { void client.refetchQueries({ queryKey: ["me"] }); }); }
  return { client, authRequests, listRequests, auth, list, cache, hidden, visible, beginAuth, adapted };
}

describe("real authentication and Explore retention boundary", () => {
  for (const scenario of fixtures.cases) {
    it(scenario.name, async () => {
      document.cookie = "peasant_token=synthetic-credential; path=/";
      const user = userEvent.setup();
      const h = mount(scenario.kind === "cold" ? scenario.route : "explore");
      await waitFor(() => expect(h.authRequests).toHaveLength(1));
      expect(h.listRequests).toHaveLength(0);
      expect(h.cache("anonymous")).toEqual([]);

      if (scenario.kind === "credential-race") {
        document.cookie = "peasant_token=replacement-credential; path=/";
        await act(async () => h.authRequests[0].resolve(json(fixtureUser("viewer-a"), scenario.status)));
        expect(await screen.findByRole("alert")).toHaveTextContent("/auth/me");
        expect(h.listRequests).toHaveLength(0);
        expect(h.cache("anonymous")).toEqual([]);
        expect(h.client.getQueryData(["me"])).toBeUndefined();
        expect(document.cookie).toContain("replacement-credential");
        await user.click(screen.getByRole("button", { name: "retry authentication" }));
        await h.auth(1, "viewer-b");
        await h.list(0, "viewer-b");
        expect(h.listRequests[0].authorization).toBe("Bearer replacement-credential");
        h.hidden("viewer-a");
        return;
      }

      if (scenario.kind === "cold") {
        if (scenario.failure !== "none") {
          const fail = async (index: number) => {
            await act(async () => scenario.failure === "network" ? h.authRequests[index].reject(new Error("network unavailable")) : h.authRequests[index].resolve(json({ error: "service unavailable" }, 500)));
            expect(await screen.findByRole("alert")).toHaveTextContent("/auth/me");
            expect(screen.getByRole("alert")).toHaveTextContent("Sessions are withheld");
          };
          await fail(0);
          await user.click(screen.getByRole("button", { name: "retry authentication" }));
          await waitFor(() => expect(h.authRequests).toHaveLength(2));
          const retry = screen.getByRole("button", { name: "retrying authentication" });
          expect(retry).toHaveAttribute("aria-disabled", "true");
          expect(retry).not.toBeDisabled();
          expect(retry).toHaveFocus();
          expect(screen.getByRole("alert")).toHaveTextContent("/auth/me");
          expect(screen.getByTestId("session-list-status")).toHaveTextContent("retrying authentication");
          await user.click(retry);
          expect(h.authRequests).toHaveLength(2);
          expect(h.listRequests).toHaveLength(0);
          expect(h.cache("anonymous")).toEqual([]);
          await fail(1);
          await user.click(screen.getByRole("button", { name: "retry authentication" }));
          await h.auth(2, scenario.target);
        } else await h.auth(0, scenario.target);
        await h.list(0, scenario.target);
        expect(screen.queryByRole("alert")).toBeNull();
        expect(h.listRequests[0].authorization).toBe(scenario.target === "anonymous" ? null : "Bearer synthetic-credential");
        expect(h.client.getQueryData(["me"])).toEqual(scenario.target === "anonymous" ? null : fixtureUser(scenario.target));
        if (scenario.target !== "anonymous") expect(h.cache("anonymous")).toEqual([]);
        return;
      }

      await h.auth(0, scenario.initial);
      const initial = await h.list(0, scenario.initial);
      expect(screen.getByTestId("auth-context")).toHaveTextContent(JSON.stringify({ user: scenario.initial, loading: false, loggedIn: true, error: false }));

      if (scenario.kind === "auth-failure") {
        h.beginAuth();
        await waitFor(() => expect(h.authRequests).toHaveLength(2));
        h.hidden(scenario.initial);
        await act(async () => h.authRequests[1].resolve(json({ error: "service unavailable" }, 500)));
        expect(await screen.findByRole("alert")).toHaveTextContent("/auth/me");
        h.hidden(scenario.initial);
        expect(h.listRequests).toHaveLength(1);
        await user.click(screen.getByRole("button", { name: "retry authentication" }));
        h.hidden(scenario.initial);
        await h.auth(2, "anonymous");
        await h.list(1, "anonymous");
        h.hidden(scenario.initial);
        expect(h.client.getQueryData(["me"])).toBeNull();
        return;
      }
      if (scenario.kind === "retention") {
        await user.click(screen.getByRole("button", { name: "page 2" }));
        await waitFor(() => expect(h.listRequests).toHaveLength(2));
        expect(h.listRequests[1].url.searchParams.get("page")).toBe("2");
        expect(screen.getByTestId("session-list-status")).toHaveTextContent("showing page 1");
        expect(h.cache(scenario.initial)).toEqual([initial]);
        h.visible(scenario.initial);
        expect(h.adapted.mock.results.at(-1)?.value.harnessFacets).toEqual(initial.harness_facets);
        await act(async () => h.listRequests[1].reject(new Error("page unavailable")));
        expect(await screen.findByRole("alert")).toHaveTextContent("previously confirmed rows are kept");
        h.visible(scenario.initial);
        await user.click(screen.getByRole("button", { name: "retry page 2" }));
        expect(screen.getByRole("button", { name: "retrying page 2" })).toHaveAttribute("aria-disabled", "true");
        expect(h.cache(scenario.initial)).toEqual([initial]);
        h.visible(scenario.initial);
        expect(h.adapted.mock.results.at(-1)?.value.harnessFacets).toEqual(initial.harness_facets);
        await h.list(2, scenario.initial);
        await user.click(screen.getByRole("radio", { name: "most tokens" }));
        await waitFor(() => {
          expect(h.listRequests.at(-1)?.url.searchParams.get("sort")).toBe("tokens");
          expect(h.listRequests.at(-1)?.url.searchParams.get("page")).toBe("1");
        });
        await h.list(h.listRequests.length - 1, scenario.initial);
        return;
      }
      if (scenario.prior_error) {
        act(() => { void h.client.refetchQueries({ queryKey: ["transcripts", scenario.initial] }); });
        await waitFor(() => expect(h.listRequests).toHaveLength(2));
        await act(async () => h.listRequests[1].reject(new Error("old viewer failure")));
        expect(await screen.findByRole("alert")).toHaveTextContent("old viewer failure");
      }
      const nextList = h.listRequests.length;
      document.cookie = `peasant_token=synthetic-${scenario.target}; path=/`;
      h.beginAuth();
      await waitFor(() => expect(h.authRequests).toHaveLength(2));
      h.hidden(scenario.initial);
      expect(screen.queryByRole("alert")).toBeNull();
      expect(h.listRequests).toHaveLength(nextList);
      expect(screen.getByTestId("auth-context")).toHaveTextContent(JSON.stringify({ user: null, loading: true, loggedIn: false, error: false }));
      h.adapted.mockClear();
      await h.auth(1, scenario.target);
      await waitFor(() => expect(h.listRequests).toHaveLength(nextList + 1));
      expect(h.listRequests[nextList].authorization).toBe(scenario.target === "anonymous" ? null : `Bearer synthetic-${scenario.target}`);
      h.hidden(scenario.initial);
      expect(h.adapted).not.toHaveBeenCalled();
      expect(h.cache(scenario.target)).toEqual([]);
      expect(screen.queryByRole("alert")).toBeNull();
      expect(screen.queryByRole("button", { name: /retry/ })).toBeNull();
      await act(async () => h.listRequests[nextList].reject(new Error("new viewer failure")));
      expect(await screen.findByRole("alert")).toHaveTextContent("new viewer failure");
      expect(screen.getByRole("alert")).not.toHaveTextContent("previously confirmed rows");
      h.hidden(scenario.initial);
      expect(h.cache(scenario.target)).toEqual([]);
      expect(h.adapted).not.toHaveBeenCalled();
      await user.click(screen.getByRole("button", { name: "retry page 1" }));
      h.hidden(scenario.initial);
      await h.list(nextList + 1, scenario.target);
      h.hidden(scenario.initial);
      expect(screen.queryByRole("alert")).toBeNull();
      if (scenario.target === "anonymous") {
        expect(h.client.getQueryData(["me"])).toBeNull();
        expect(h.listRequests[nextList].authorization).toBeNull();
        expect(screen.getByTestId("auth-context")).toHaveTextContent(JSON.stringify({ user: null, loading: false, loggedIn: false, error: false }));
      }
    });
  }
});
