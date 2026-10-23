import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  installMountedProfileTeardown,
  installProfileRESTFixture,
  renderProfileRoute,
} from "@/test/mountedProfileRoute";
import { leaveForMarketingSite } from "@/lib/marketing";

// Mounts the REAL profile route for its owner and walks the account deletion:
// it is asked in fairtrade's consent dialog, never the browser's own confirm,
// and the request goes only after the owner ticks that it cannot be undone.
// Leaving for the marketing site afterwards is the stubbed boundary.

const OWNER = "fixture-owner";

installMountedProfileTeardown();

beforeEach(() => vi.mocked(leaveForMarketingSite).mockClear());

describe("deleting the account from the profile", () => {
  it("asks in the consent dialog and deletes only after the owner consents", async () => {
    const nativeConfirm = vi.fn(() => true);
    vi.stubGlobal("confirm", nativeConfirm);
    installProfileRESTFixture({ profileUsername: OWNER, viewerUsername: OWNER, contributions: [] });
    await renderProfileRoute(OWNER);

    fireEvent.click(await screen.findByRole("button", { name: "delete account" }));
    const dialog = await screen.findByRole("dialog", { name: "delete your account?" });
    expect(nativeConfirm).not.toHaveBeenCalled();
    expect(dialog).toHaveTextContent("the transcripts you published and the collectives you own");

    const confirm = within(dialog).getByRole("button", { name: "delete account" });
    expect(confirm, "the delete waits for consent").toBeDisabled();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: "i understand this cannot be undone" }));
    expect(confirm).toBeEnabled();
    fireEvent.click(confirm);

    await waitFor(() => expect(vi.mocked(leaveForMarketingSite)).toHaveBeenCalledTimes(1));
  });

  it("closes without deleting when the owner cancels", async () => {
    installProfileRESTFixture({ profileUsername: OWNER, viewerUsername: OWNER, contributions: [] });
    await renderProfileRoute(OWNER);

    fireEvent.click(await screen.findByRole("button", { name: "delete account" }));
    const dialog = await screen.findByRole("dialog", { name: "delete your account?" });
    fireEvent.click(within(dialog).getByRole("button", { name: "cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(vi.mocked(leaveForMarketingSite)).not.toHaveBeenCalled();
  });
});
