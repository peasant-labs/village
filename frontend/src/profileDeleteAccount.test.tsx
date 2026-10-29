import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { installMountedProfileTeardown, installProfileRESTFixture, renderProfileRoute } from "@/test/mountedProfileRoute";

const OWNER = "fixture-owner";
installMountedProfileTeardown();

describe("profile account controls live in settings", () => {
  it("leads the owner to settings without duplicating privacy or account deletion", async () => {
    installProfileRESTFixture({ profileUsername: OWNER, viewerUsername: OWNER, contributions: [] });
    await renderProfileRoute(OWNER);
    expect(await screen.findByRole("link", { name: "settings" })).toHaveAttribute("href", "/settings");
    expect(screen.queryByText("discoverable profile")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "delete account" })).not.toBeInTheDocument();
  });

  it("offers no account settings control on somebody else's profile", async () => {
    installProfileRESTFixture({ profileUsername: OWNER, viewerUsername: "other-reader", contributions: [] });
    await renderProfileRoute(OWNER);
    expect(await screen.findByTestId("profile-page")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "settings" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "delete account" })).not.toBeInTheDocument();
  });
});
