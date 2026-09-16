import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installAttachmentSurfacesTeardown,
  installGroupSettingsREST,
  renderGroupSettingsRoute,
} from "@/test/mountedAttachmentSurfaces";

installAttachmentSurfacesTeardown();

const GROUP_ID = "44444444-4444-4444-4444-444444444444";

describe("the collective prompt check settings", () => {
  it("reads both settings from the group and saves them through the update", async () => {
    const requests = installGroupSettingsREST({
      id: GROUP_ID,
      ownerUsername: "owner",
      postPromptsCheck: true,
      promptsCheckMode: "informational",
    });
    await renderGroupSettingsRoute(GROUP_ID);

    // Both controls are prefilled from the group the server returned, which the
    // saved body proves: the switch flips OFF from the fixture's true and the
    // mode moves off the fixture's informational.
    const toggle = await screen.findByRole("switch", { name: /Post a prompts check/ });
    const informational = screen.getByRole("radio", { name: /Informational/ }) as HTMLInputElement;
    expect(informational.checked).toBe(true);

    fireEvent.click(toggle);
    fireEvent.click(screen.getByRole("radio", { name: /Required/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(requests.some((r) => r.method === "PATCH" && r.url.includes("/groups/"))).toBe(true);
    });
    const patch = requests.find((r) => r.method === "PATCH");
    expect(patch?.body).toMatchObject({
      post_prompts_check: false,
      prompts_check_mode: "required",
    });
  });
});
