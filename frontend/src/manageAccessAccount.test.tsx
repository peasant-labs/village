import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { clearAuthTokenCookie, setAuthTokenCookie } from "@/lib/api";
import { installRESTFixture, renderProductionRoute } from "@/test/mountedProductionRoute";
import { loadManageAccessAccountFixtures } from "@/test/manageAccessAccountFixtures";

const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { "content-type": "application/json" } });
const detail: SessionDetailPayload = { id: "account-read-session", harness: "claude-code", startTime: "2026-08-21T09:00:00Z", endTime: "2026-08-21T09:00:00Z", durationMins: 0, totalTokens: 0, tokensIn: 0, tokensOut: 0, turnCount: 0, toolCallCount: 0, project: "village", model: "anthropic/claude-fable-5", turns: [] };
const privateCollective = "Earlier Private Collective";
afterEach(() => { cleanup(); vi.unstubAllGlobals(); clearAuthTokenCookie(); });
async function openAccess() {
  fireEvent.click(await screen.findByRole("button", { name: "more" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "manage access" }));
  return screen.findByRole("dialog", { name: "manage access" });
}
for (const testCase of loadManageAccessAccountFixtures()) {
  it(testCase.name, async () => {
    const viewer = { id: "earlier-owner", github_username: "EarlierOwner", orgs: [] };
    const metadata = { transcript: { id: "earlier-transcript", local_id: detail.id, visibility: "shared" as const, title: "earlier owned session", description: null, project_name: "village" }, owner: viewer, enriched_shares: [], viewer_collectives: [] };
    installRESTFixture(metadata.transcript.id, metadata, detail, "earlier account access", viewer, (url) => {
      const path = new URL(url).pathname.replace(/^\/api\/v1/, "");
      if (path === "/groups") return json([{ id: "private-group", name: privateCollective, role: "member", acceptance_mode: "open", member_count: 1 }]);
      if (path === "/users/me/collectives/contributions") return json({ collectives: [{ id: "private-group", name: privateCollective, approved_count: 0, pending_count: 1, rejected_attempt_count: 0, withdrawn_attempt_count: 0 }] });
      if (path === "/groups/private-group/my-shares") return json([{ id: metadata.transcript.id, status: "pending" }]);
    });
    setAuthTokenCookie("earlier-credential");
    const client = await renderProductionRoute(metadata.transcript.id, "", { signedIn: true });
    const earlierDialog = await openAccess();
    await within(earlierDialog).findByText(privateCollective);
    await within(earlierDialog).findByText("adding · waits for approval");
    cleanup();

    const nextViewer = testCase.change === "actor" ? { ...viewer, id: "current-owner", github_username: "CurrentOwner" } : viewer;
    const nextMetadata = { ...metadata, transcript: { ...metadata.transcript, id: "current-transcript", title: "current owned session" }, owner: nextViewer };
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    let currentReads = 0;
    installRESTFixture(nextMetadata.transcript.id, nextMetadata, detail, "current account access", nextViewer, async (url) => {
      const path = new URL(url).pathname.replace(/^\/api\/v1/, "");
      if (path === "/groups" || path === "/users/me/collectives/contributions") {
        currentReads++;
        await held;
        return json(path === "/groups" ? [] : { collectives: [] });
      }
      if (path === "/groups/private-group/my-shares") return json([]);
    });
    if (testCase.change === "credential") setAuthTokenCookie("current-credential");
    await renderProductionRoute(nextMetadata.transcript.id, "", { signedIn: true, client });
    await screen.findAllByText("current owned session");
    const currentDialog = await openAccess();
    await waitFor(() => expect(currentReads).toBeGreaterThanOrEqual(2));
    expect(currentDialog).not.toHaveTextContent(privateCollective);
    expect(within(currentDialog).queryByRole("button", { name: `add ${privateCollective}` })).toBeNull();
    await act(async () => { release(); });
    await within(currentDialog).findByPlaceholderText("collective or github org");
    expect(currentDialog).not.toHaveTextContent(privateCollective);
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain("credential");
  });
}
