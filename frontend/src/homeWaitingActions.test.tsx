import { expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { readFixture, assertCaseNames } from "@/test/mountedCollectivesSurfaces";
import type { HomeCollectiveCase } from "@/test/homePageFixtures";

const fixture = readFixture("home-waiting-actions.yaml") as {
  collective: { id: string; name: string; pending: number };
  viewer: string;
  cases: { name: string; role: HomeCollectiveCase["role"] | null; label: string; href: string; copy: string }[];
};
assertCaseNames("home-waiting-actions.yaml", fixture.cases.map((row) => row.name), [
  "an-owner-opens-the-real-review-queue",
  "a-member-opens-their-own-submission-ledger",
  "a-contributor-does-not-gain-review-authority",
  "a-former-member-can-still-find-their-own-submission",
]);
installHomeRouteTeardown();

it.each(fixture.cases)("$name", async (row) => {
  installHomeRouteREST({
    viewerUsername: fixture.viewer,
    transcripts: [],
    contributions: [fixture.collective],
    collectives: row.role ? [{ ...fixture.collective, role: row.role, members: 3 }] : [],
  });
  await renderAppRoute("/");
  const section = await screen.findByTestId("home-waiting");
  const action = await within(section).findByRole("link", { name: row.label });
  expect(action).toHaveAttribute("href", row.href);
  expect(section).toHaveTextContent(row.copy);
  expect(within(section).queryByRole("button", { name: /approve|reject/ })).not.toBeInTheDocument();
});
