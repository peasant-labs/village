import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { loadPullRequestPageFixtures } from "@/test/pullRequestPageFixtures";
import {
  installAttachmentSurfacesTeardown,
  installPullRequestREST,
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
      is_private_repository: row.is_private_repository,
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
    digest:
      row.digest === "present"
        ? makeDigest(row.digest_transcript_ids)
        : row.digest === "empty"
          ? makeDigest([])
          : null,
    transcripts: row.transcript_ids.map((transcriptId, index) => ({
      transcript_id: transcriptId,
      position: index,
      previous_visibility: "private",
      title: index === 0 ? "picker work" : `session ${index + 1}`,
      session_start: "2026-01-01T00:00:00Z",
    })),
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

      // The digest is the design system's component, so assert its content
      // rather than its markup: the prompt text it renders, and that it mounted.
      if (row.digest === "present") {
        expect(await screen.findByText("please add the picker")).toBeTruthy();
        await waitFor(() => expect(document.querySelector(".pd")).toBeTruthy());
      } else if (row.digest === "empty") {
        expect(document.querySelector(".pd")).toBeNull();
        expect(screen.getByTestId("digest-empty").textContent).toContain(
          "No prompts are available for this pull request.",
        );
      } else {
        expect(document.querySelector(".pd")).toBeNull();
        // The sentence naming who the digest is shown to must match the
        // repository: a private repository's digest never goes to just anyone.
        const sentence = screen.getByText(/shown to the pull request's author/);
        expect(sentence.textContent).toContain(
          row.is_private_repository ? "members of this collective" : "anyone",
        );
        if (row.is_private_repository) {
          expect(sentence.textContent).toContain("this repository's collaborators");
        }
      }

      // A bound transcript the digest no longer advertises is marked, and only
      // that one: the reader is told something is missing without being told
      // which transcript or why.
      expect(screen.queryAllByText("not available").length).toBe(
        row.expect_unavailable_marks,
      );

      // Confirm and detach appear only where the state and the viewer allow.
      expect(screen.queryByTestId("confirm-attachment") !== null).toBe(row.expect_confirm);
      expect(screen.queryByTestId("detach-attachment") !== null).toBe(row.expect_detach);

      // Where the author is asked to confirm, they are told the audience the
      // attach makes the transcripts readable by, and only that one: a public
      // repository must not be described in a private one's terms or the other
      // way around. Everywhere else there is no statement to read.
      const audience = screen.queryByTestId("attachment-audience");
      if (row.expect_audience === "none") {
        expect(audience).toBeNull();
      } else if (row.expect_audience === "anyone") {
        expect(audience?.textContent).toContain("readable by anyone");
        expect(audience?.textContent ?? "").not.toContain("members of this collective");
        expect(audience?.textContent ?? "").not.toContain("collaborators");
      } else {
        // A private repository's audience is the collective AND the repository's
        // own collaborators, and both are named: a reader deciding whether to
        // attach is entitled to the whole audience, not the reassuring half.
        expect(audience?.textContent).toContain("readable by members of this collective");
        expect(audience?.textContent).toContain("by this repository's collaborators");
        expect(audience?.textContent ?? "").not.toContain("readable by anyone");
      }
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

  it("renders the chain and links each item where it belongs", async () => {
    const row = loadPullRequestPageFixtures().find(
      (c) => c.name === "author viewing an attached digest",
    );
    if (!row) throw new Error("the attached case is missing from the fixture");
    installPullRequestREST(fixtureFor(row));
    await renderPullRequestRoute(OWNER, NAME, NUMBER);
    await screen.findByTestId("pull-request-title");
    await waitFor(() => expect(document.querySelector(".pd")).toBeTruthy());

    // The commit anchor is the only link that leaves Village, and it resolves
    // through the attachment's repository rather than a caller-supplied string.
    const commit = document.querySelector('.pd a[href*="/commit/"]');
    expect(commit?.getAttribute("href")).toBe(
      `https://github.com/${OWNER}/${NAME}/commit/abc1234000000000000000000000000000000001`,
    );
    // A prompt and a skill both open the exact turn.
    const turnLinks = [...document.querySelectorAll('.pd a[href*="?turn="]')].map((a) =>
      a.getAttribute("href"),
    );
    expect(turnLinks).toContain("/transcripts/11111111-1111-1111-1111-111111111111?turn=0");
    expect(document.querySelector(".pd")?.textContent).toContain("/commit");
    // The attached-transcripts list is this page's own, not the digest's.
    expect(screen.getByText("picker work")).toBeTruthy();
  });
});
