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

  // A save must not claim the linked org. The contract reads a value as a
  // request to link it, which the update path refuses unless the caller has
  // marked the org visible, and the install handshake is what writes it. An
  // omitted field is the contract's preserve.
  it("saves without claiming the linked org it records", async () => {
    const requests = installGroupSettingsREST({
      id: GROUP_ID,
      ownerUsername: "owner",
      postPromptsCheck: true,
      promptsCheckMode: "informational",
      linkedGithubOrg: "acme",
    });
    await renderGroupSettingsRoute(GROUP_ID);

    await screen.findByRole("switch", { name: /Post a prompts check/ });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(requests.some((r) => r.method === "PATCH")).toBe(true);
    });
    const patch = requests.find((r) => r.method === "PATCH");
    expect(patch?.body).not.toHaveProperty("linked_github_org");
  });

  // The linked org is a fact of the App installation, so the settings page no
  // longer offers a control for it: the only options came from orgs the caller
  // had marked visible, which nothing could mark, so the control could never be
  // set and would misreport a collective that is bound.
  it("offers no control for the linked org", async () => {
    installGroupSettingsREST({
      id: GROUP_ID,
      ownerUsername: "owner",
      postPromptsCheck: true,
      promptsCheckMode: "informational",
    });
    await renderGroupSettingsRoute(GROUP_ID);

    await screen.findByRole("switch", { name: /Post a prompts check/ });
    expect(screen.queryByLabelText(/Link to GitHub org/i)).toBeNull();
  });
});
