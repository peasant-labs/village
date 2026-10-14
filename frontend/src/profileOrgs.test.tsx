import { waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installMountedProfileTeardown,
  installProfileRESTFixture,
  renderProfileRoute,
} from "@/test/mountedProfileRoute";
import { loadProfileOrgsFixtures } from "@/test/profileOrgsFixtures";

// Mounts the REAL profile route — the surface a visitor loads — and asserts the
// linked organizations on it. The viewer is anonymous in every case: the linked
// list is public, so a case that needed to be signed in could not show that.

const fixtures = loadProfileOrgsFixtures();
const OWNER = "fixture-owner";

installMountedProfileTeardown();

function orgRows(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>('[data-testid="profile-org"]')];
}

describe("mounted profile route: the organizations linked to the profile", () => {
  for (const c of fixtures) {
    it(c.name, async () => {
      const requested = installProfileRESTFixture({
        profileUsername: OWNER,
        viewerUsername: null,
        contributions: [],
        publicOrgs: c.orgs,
      });
      await renderProfileRoute(OWNER);

      // The page asks the org read for every viewer, and the profile's own
      // visibility is decided by the server at the profile read, not here.
      await waitFor(() => expect(requested).toContain(`/users/${OWNER}/orgs`));

      if (!c.expect_section) {
        expect(document.querySelector('[data-testid="profile-orgs"]')).toBeNull();
        expect(orgRows()).toHaveLength(0);
        return;
      }

      await waitFor(() => expect(orgRows()).toHaveLength(c.expected_logins.length));
      // Order and membership are the server's: the page lists what it was sent,
      // in the order it was sent.
      expect(orgRows().map((row) => (row.textContent ?? "").trim())).toEqual(c.expected_logins);
    });
  }
});
