import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import RootPage from "@/app/page";
import WelcomePage from "@/app/welcome/page";
import GroupsPage from "@/app/groups/page";
import PublishPage from "@/app/publish/page";
import { AuthProvider, useAuth } from "./AuthProvider";
import { loadAuthBackgroundFixtures } from "@/test/authBackgroundFixtures";
import { fixtureUser, loadExploreViewerFixtures, viewerResponse, type Viewer } from "@/test/exploreViewerFixtures";

const router = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => router }));
const clients: QueryClient[] = [];
afterEach(() => { cleanup(); clients.splice(0).forEach((c) => c.clear()); vi.unstubAllGlobals(); vi.clearAllMocks(); document.cookie = "peasant_token=; path=/; max-age=0"; });
function json(body: unknown, status = 200) { return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } }); }
function deferred() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((yes) => { resolve = yes; });
  return { promise, resolve };
}
function Identity() {
  const auth = useAuth();
  return <><output data-testid="established-identity">{JSON.stringify({ user: auth.user?.id ?? null, loading: auth.isLoading, loggedIn: auth.isLoggedIn, error: auth.isError })}</output><output data-testid="current-verification">{String(auth.isVerified)}</output></>;
}
describe("established production routes survive background identity checks", () => {
  for (const entry of loadAuthBackgroundFixtures()) it(entry.name, async () => {
    const viewer = entry.route === "anonymous-root" ? "anonymous" : "viewer-a";
    const identity = { ...fixtureUser(viewer), username_chosen: entry.route !== "welcome" };
    const data = loadExploreViewerFixtures().viewers[viewer];
    let responseViewer: Viewer = viewer;
    const heldLists: (ReturnType<typeof deferred> & { body: ReturnType<typeof viewerResponse> })[] = [];
    let holdLists = false;
    const authRequests: ReturnType<typeof deferred>[] = [];
    const listRequests: URL[] = [];
    let failPage = entry.retained_failure;
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
    clients.push(client);
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const url = new URL(String(input));
      if (url.pathname.endsWith("/auth/me")) { const request = deferred(); authRequests.push(request); return request.promise; }
      if (url.pathname.endsWith("/transcripts")) {
        listRequests.push(url);
        if (failPage && url.searchParams.get("page") === "2") return Promise.resolve(json({ error: "retained page failure" }, 500));
        const body = viewerResponse(loadExploreViewerFixtures().viewers[responseViewer], responseViewer, Number(url.searchParams.get("page") ?? 1));
        body.limit = Number(url.searchParams.get("limit") ?? 24);
        if (holdLists) { const request = { ...deferred(), body }; heldLists.push(request); return request.promise; }
        return Promise.resolve(json(body));
      }
      if (url.pathname.endsWith("/groups/search")) return Promise.resolve(json({ collectives: [] }));
      if (url.pathname.endsWith("/collectives")) return Promise.resolve(json({ collectives: [] }));
      if (url.pathname.endsWith("/groups/visible") || url.pathname.endsWith("/auth/orgs") || url.pathname.endsWith("/tags/popular")) return Promise.resolve(json([]));
      throw new Error(`unexpected lifecycle request ${url.pathname}`);
    }));
    const user = userEvent.setup();
    render(<QueryClientProvider client={client}><AuthProvider><Identity />{entry.route === "welcome" ? <WelcomePage /> : entry.route === "groups" ? <GroupsPage /> : entry.route === "publish" ? <PublishPage /> : <RootPage />}</AuthProvider></QueryClientProvider>);
    const respond = async (index: number, failure = false) => {
      await waitFor(() => expect(authRequests[index]).toBeDefined());
      await act(async () => authRequests[index].resolve(failure ? json({ error: "authentication unavailable" }, 500) : viewer === "anonymous" ? json({ error: "anonymous" }, 401) : json(identity)));
    };
    await respond(0);
    await waitFor(() => expect(screen.getByTestId("current-verification")).toHaveTextContent("true"));
    let control: HTMLElement;
    switch (entry.route) {
      case "anonymous-root":
        await screen.findByText(data.title, { exact: false });
        await user.click(screen.getByRole("radio", { name: "most tokens" }));
        await waitFor(() => expect(listRequests.at(-1)?.searchParams.get("sort")).toBe("tokens"));
        await user.click(screen.getByRole("button", { name: "page 2" }));
        await waitFor(() => expect(listRequests.at(-1)?.searchParams.get("page")).toBe("2"));
        if (entry.retained_failure) expect(await screen.findByRole("alert")).toHaveTextContent("retained page failure");
        control = screen.getByRole("radio", { name: "most tokens" });
        break;
      case "signed-root":
        control = await screen.findByRole("link", { name: /viewer a private marker/i });
        break;
      case "welcome":
        control = await screen.findByRole("textbox");
        await user.clear(control);
        await user.type(control, "edited-handle");
        break;
      case "groups":
        await user.click(await screen.findByRole("button", { name: /new collective/i }));
        control = screen.getByRole("textbox", { name: /name/i });
        await user.type(control, "edited collective");
        break;
      case "publish":
        await user.click(await screen.findByRole("button", { name: "Import" }));
        control = screen.getByRole("dialog", { name: "How to import transcripts" });
        within(control).getAllByRole("button", { name: "Close" })[0].focus();
        break;
    }
    const intent = listRequests.at(-1)?.search;
    const requestCount = listRequests.length;
    act(() => { void client.invalidateQueries({ queryKey: ["me"] }); });
    await waitFor(() => expect(authRequests).toHaveLength(2));
    // DOM identity, not merely the reappearance of an equivalent control.
    expect(control.isConnected).toBe(true);
    expect(screen.getByTestId("current-verification")).toHaveTextContent("false");
    expect(screen.queryByTestId("root-route-pending")).toBeNull();
    expect(screen.getByTestId("established-identity")).toHaveTextContent(JSON.stringify({ user: viewer === "anonymous" ? null : viewer, loading: false, loggedIn: viewer !== "anonymous", error: false }));
    expect(router.replace).not.toHaveBeenCalled();
    if (entry.route === "anonymous-root") {
      expect(document.querySelector(`a[href="/transcripts/${data.row}"]`)).toBeNull();
      expect(listRequests).toHaveLength(requestCount);
    }
    if (entry.outcome === "viewer-change" || entry.outcome === "expired") {
      responseViewer = entry.outcome === "viewer-change" ? "viewer-b" : "anonymous";
      holdLists = true;
      await act(async () => authRequests[1].resolve(responseViewer === "anonymous" ? json({ error: "expired" }, 401) : json(fixtureUser(responseViewer))));
      await waitFor(() => expect(heldLists).toHaveLength(1));
      expect(control.isConnected).toBe(false);
      expect(document.querySelector(`a[href="/transcripts/${data.row}"]`)).toBeNull();
      expect(screen.getByTestId("established-identity")).toHaveTextContent(JSON.stringify({ user: responseViewer === "anonymous" ? null : responseViewer, loading: false, loggedIn: responseViewer !== "anonymous", error: false }));
      expect(listRequests.at(-1)?.searchParams.get("owner")).toBe(responseViewer === "anonymous" ? null : responseViewer);
      await act(async () => heldLists[0].resolve(json(heldLists[0].body)));
      await waitFor(() => expect(document.querySelector(`a[href="/transcripts/${loadExploreViewerFixtures().viewers[responseViewer].row}"]`)).not.toBeNull());
      expect(document.querySelector(`a[href="/transcripts/${data.row}"]`)).toBeNull();
      expect(client.getQueriesData({ queryKey: ["transcripts"] }).map(([, body]) => body)).toContainEqual(heldLists[0].body);
      return;
    }
    await respond(1, entry.outcome === "failure");
    await waitFor(() => expect(client.getQueryState(["me"])?.fetchStatus).toBe("idle"));
    expect(control.isConnected).toBe(true);
    expect(router.replace).not.toHaveBeenCalled();
    if (entry.outcome === "failure") {
      expect(screen.getByTestId("current-verification")).toHaveTextContent("false");
      expect(screen.getByTestId("established-identity")).toHaveTextContent(JSON.stringify({ user: viewer === "anonymous" ? null : viewer, loading: false, loggedIn: viewer !== "anonymous", error: true }));
      act(() => { void client.invalidateQueries({ queryKey: ["me"] }); });
      await respond(2);
    }
    await waitFor(() => expect(screen.getByTestId("established-identity")).toHaveTextContent('"error":false'));
    expect(screen.getByTestId("current-verification")).toHaveTextContent("true");
    expect(control.isConnected).toBe(true);
    switch (entry.route) {
      case "anonymous-root":
        expect(control).toBeChecked();
        expect(listRequests.at(-1)?.search).toBe(intent);
        if (entry.retained_failure) {
          expect(await screen.findByRole("alert")).toHaveTextContent("retained page failure");
          failPage = false;
          await user.click(screen.getByRole("button", { name: "retry page 2" }));
        }
        await waitFor(() => expect(document.querySelector(`a[href="/transcripts/${data.row}"]`)).not.toBeNull());
        expect(listRequests.at(-1)?.searchParams.get("page")).toBe("2");
        expect(listRequests.at(-1)?.searchParams.get("sort")).toBe("tokens");
        break;
      case "welcome": expect(control).toHaveValue("edited-handle"); break;
      case "groups": expect(control).toHaveValue("edited collective"); break;
      case "publish": expect(within(control).getAllByRole("button", { name: "Close" })[0]).toHaveFocus(); break;
      case "signed-root": expect(control).toBeVisible(); break;
    }
  });
});
