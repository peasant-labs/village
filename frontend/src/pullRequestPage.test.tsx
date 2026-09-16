import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { loadPullRequestPageFixtures } from "@/test/pullRequestPageFixtures";
import {
  installAttachmentSurfacesTeardown,
  installPullRequestREST,
  makeAttachmentResponse,
  makeDigest,
  renderPullRequestRoute,
  type PullRequestFixture,
} from "@/test/mountedAttachmentSurfaces";

installAttachmentSurfacesTeardown();

const OWNER = "acme";
const NAME = "widgets";
const NUMBER = 7;

/** One fixture row, as the page's route would receive it. */
function fixtureFor(
  row: ReturnType<typeof loadPullRequestPageFixtures>[number],
): PullRequestFixture {
  return {
    owner: OWNER,
    name: NAME,
    number: NUMBER,
    viewerIsAuthor: row.viewer_is_author,
    attachment: {
      id: "7f3c1a2b-4d5e-4f60-8a9b-0c1d2e3f4a5b",
      owner: OWNER,
      name: NAME,
      number: NUMBER,
      head_sha: "0123456789abcdef0123456789abcdef01234567",
      is_private_repository: true,
      state: row.state,
      author_user_id: "11111111-1111-1111-1111-111111111111",
      requested_by_github_id: null,
      comment_id: null,
      check_run_id: null,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      confirmed_at: null,
      detached_at: null,
    },
    digest: row.digest === "present" ? makeDigest() : null,
    transcripts:
      row.state === "attached"
        ? [
            {
              transcript_id: "11111111-1111-1111-1111-111111111111",
              position: 0,
              previous_visibility: "private",
              title: "picker work",
              session_start: "2026-01-01T00:00:00Z",
            },
          ]
        : [],
    confirmStatus: row.confirm_status ?? undefined,
    confirmMessage: "This pull request is not in a state that allows that action",
  };
}

describe("the pull request page", () => {
  for (const row of loadPullRequestPageFixtures()) {
    it(`renders ${row.name}`, async () => {
      const fixture = fixtureFor(row);
      installPullRequestREST(fixture);
      await renderPullRequestRoute(OWNER, NAME, NUMBER);

      // The header names the pull request whatever the state is. It is the first
      // thing that appears once the attachment has loaded, so awaiting it also
      // settles the query the rest of the assertions read.
      const title = await screen.findByTestId("pull-request-title");
      expect(title.textContent).toBe(`${OWNER}/${NAME} #${NUMBER}`);

      // The digest, when the server sent one, renders its counts and its chain.
      if (row.digest === "present") {
        expect(await screen.findByText(/1 sessions/)).toBeTruthy();
        expect(screen.getByText("please add the picker")).toBeTruthy();
        expect(screen.getByText("session 1")).toBeTruthy();
      } else {
        expect(screen.queryByText(/1 sessions/)).toBeNull();
        expect(screen.getByText(/shown to the pull request's author/)).toBeTruthy();
      }

      // Confirm and detach appear only where the state and the viewer allow.
      expect(screen.queryByTestId("confirm-attachment") !== null).toBe(row.expect_confirm);
      expect(screen.queryByTestId("detach-attachment") !== null).toBe(row.expect_detach);
    });
  }

  it("shows the server's message when a confirm is refused with a conflict", async () => {
    const row = loadPullRequestPageFixtures().find(
      (c) => c.name === "confirm refused with a conflict",
    );
    if (!row) throw new Error("the conflict case is missing from the fixture");
    installPullRequestREST(fixtureFor(row));
    await renderPullRequestRoute(OWNER, NAME, NUMBER);

    const confirm = await screen.findByTestId("confirm-attachment");
    fireEvent.click(confirm);

    await waitFor(() => {
      const message = screen.getByTestId("attachment-action-error");
      expect(message.textContent).toContain(
        "This pull request is not in a state that allows that action",
      );
    });
  });

  it("replaces the cached attachment with the server's answer on detach", async () => {
    const row = loadPullRequestPageFixtures().find(
      (c) => c.name === "author viewing an attached digest",
    );
    if (!row) throw new Error("the attached case is missing from the fixture");
    const requests = installPullRequestREST(fixtureFor(row));
    await renderPullRequestRoute(OWNER, NAME, NUMBER);

    fireEvent.click(await screen.findByTestId("detach-attachment"));

    await waitFor(() => {
      expect(requests.some((r) => r.method === "DELETE")).toBe(true);
    });
    // The response the server returned is what the page now shows, so the state
    // it renders can never be one the server has moved past.
    await waitFor(() => {
      expect(screen.getByText("detached")).toBeTruthy();
    });
  });

  it("renders every digest item kind and links a commit to GitHub", async () => {
    const row = loadPullRequestPageFixtures().find(
      (c) => c.name === "author viewing an attached digest",
    );
    if (!row) throw new Error("the attached case is missing from the fixture");
    installPullRequestREST(fixtureFor(row));
    await renderPullRequestRoute(OWNER, NAME, NUMBER);
    await screen.findByTestId("pull-request-title");

    // The skill marker is a name plus a count inside one link, so match on the
    // link's own text rather than on a fragment of it.
    const skill = screen.getByText(
      (_content, element) =>
        element?.tagName === "A" && element.textContent?.startsWith("/commit") === true,
    );
    expect(skill.textContent).toBe("/commit -m seed");
    // The skills summary above the chain carries the invocation count.
    expect(screen.getByText("×1")).toBeTruthy();

    const commit = screen.getByText("abc1234");
    expect(commit.getAttribute("href")).toBe(
      `https://github.com/${OWNER}/${NAME}/commit/abc1234000000000000000000000000000000001`,
    );
    const prompt = screen.getByText("please add the picker").closest("a");
    expect(prompt?.getAttribute("href")).toContain("?turn=0");
    expect(screen.getByText("picker work")).toBeTruthy();
  });
});
