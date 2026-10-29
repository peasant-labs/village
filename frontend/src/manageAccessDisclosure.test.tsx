import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import ConfirmContributeDialog from "@/components/transcript/ConfirmContributeDialog";
import TranscriptEditDialog from "@/components/transcript/TranscriptEditDialog";
import { clearAuthTokenCookie, setAuthTokenCookie } from "@/lib/api";
import { loadDisclosureFixtures, type DisclosureCase } from "@/test/integrityDisclosureFixtures";
import { installRESTFixture, renderProductionRoute } from "@/test/mountedProductionRoute";

// Every place a transcript becomes readable by anyone besides its owner carries
// the same disclosure line. Mounted surfaces only: the manage access dialog on
// the real transcript route, the contribute confirmation, and the edit dialog's
// public choice.

const fixtures = loadDisclosureFixtures();
const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { "content-type": "application/json" } });
const detail: SessionDetailPayload = { id: "disclosure-session", harness: "claude-code", startTime: "2026-10-29T09:00:00Z", endTime: "2026-10-29T09:00:00Z", durationMins: 0, totalTokens: 0, tokensIn: 0, tokensOut: 0, turnCount: 0, toolCallCount: 0, project: "village", model: "anthropic/claude-fable-5", turns: [] };

afterEach(() => { cleanup(); vi.unstubAllGlobals(); clearAuthTokenCookie(); });

const flat = (el: Element) => (el.textContent ?? "").replace(/\s+/g, " ").trim();

async function mountSurface(c: DisclosureCase): Promise<HTMLElement> {
  if (c.surface === "contribute_confirm") {
    render(
      <ConfirmContributeDialog
        open
        onClose={() => {}}
        onConfirm={() => {}}
        transcripts={[{ id: "t1", title: "a session" }]}
        collectives={[{ id: "g1", name: "lab" }]}
      />,
    );
    return screen.findByRole("dialog");
  }
  if (c.surface === "share_choice" || c.surface === "already_public") {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <TranscriptEditDialog open onClose={() => {}} transcriptId="t1" initialTitle="a session" initialVisibility={c.surface === "share_choice" ? "shared" : "public"} />
      </QueryClientProvider>,
    );
    return screen.findByRole("dialog");
  }
  if (c.surface === "make_public") {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <TranscriptEditDialog open onClose={() => {}} transcriptId="t1" initialTitle="a session" initialVisibility="private" />
      </QueryClientProvider>,
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).not.toContain(fixtures.linkLabel);
    fireEvent.click(within(dialog).getByRole("button", { name: /public/ }));
    return dialog;
  }
  const viewer = { id: "owner", github_username: "Owner", orgs: [] };
  const metadata = { transcript: { id: "disclosed", local_id: detail.id, visibility: c.visibility, title: "disclosed session", description: null, project_name: "village" }, owner: viewer, enriched_shares: [], viewer_collectives: [] };
  installRESTFixture(metadata.transcript.id, metadata, detail, "manage access disclosure", viewer, (url) => {
    const path = new URL(url).pathname.replace(/^\/api\/v1/, "");
    if (path === "/collectives") return json([]);
    if (path === "/users/me/collectives/contributions") return json({ collectives: [] });
  });
  setAuthTokenCookie("disclosure-credential");
  await renderProductionRoute(metadata.transcript.id, "", { signedIn: true });
  fireEvent.click(await screen.findByRole("button", { name: "more" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "manage access" }));
  return screen.findByRole("dialog", { name: "manage access" });
}

for (const c of fixtures.cases) {
  it(c.name, async () => {
    const dialog = await mountSurface(c);
    const text = flat(dialog);
    switch (c.check) {
      case "shown":
        expect(text).toContain(`${fixtures.line} ${fixtures.linkLabel}.`);
        break;
      case "link": {
        const link = within(dialog).getByRole("link", { name: fixtures.linkLabel });
        expect(link).toHaveAttribute("href", fixtures.href);
        break;
      }
      case "no_middle_dot":
        expect(fixtures.line).not.toContain("\u00b7");
        expect(text).not.toContain("\u00b7");
        break;
      case "no_access_claim":
        for (const phrase of fixtures.forbidden) expect(text).not.toContain(phrase);
        break;
    }
  });
}
