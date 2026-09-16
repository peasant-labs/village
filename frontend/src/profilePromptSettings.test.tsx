import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installAttachmentSurfacesTeardown,
  installProfileSettingsREST,
  renderProfileSettingsRoute,
} from "@/test/mountedAttachmentSurfaces";

installAttachmentSurfacesTeardown();

describe("the preview-before-attaching preference", () => {
  it("reads the caller's own setting and writes it back", async () => {
    const requests = installProfileSettingsREST({ username: "owner", previewBeforeAttach: true });
    await renderProfileSettingsRoute("owner");

    // The control reads /users/me/settings, not /auth/me: the request and the
    // flipped value below both come from that route.
    const input = await screen.findByRole("switch", { name: /Preview before attaching/ });
    expect(requests.some((r) => r.method === "GET" && r.url.endsWith("/users/me/settings"))).toBe(
      true,
    );

    fireEvent.click(input);
    await waitFor(() => {
      expect(
        requests.some((r) => r.method === "PATCH" && r.url.endsWith("/users/me/settings")),
      ).toBe(true);
    });
    const patch = requests.find((r) => r.method === "PATCH");
    expect(patch?.body).toEqual({ preview_before_attach: false });
  });
});
