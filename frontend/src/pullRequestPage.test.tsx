import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  PULL_REQUEST_CONTROLS,
  loadPullRequestPageFixtures,
  pullRequestPageCase,
  type PullRequestPageCase,
} from "@/test/pullRequestPageFixtures";
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
function fixtureFor(row: PullRequestPageCase): PullRequestFixture {
  const commits = { covered: row.commits_covered, total: row.commits_total };
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
      title: row.pull_title,
      head_ref: row.head_ref,
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
        ? makeDigest(row.digest_transcripts, commits)
        : row.digest === "empty"
          ? makeDigest([], commits)
          : null,
    transcripts: row.bound_transcripts.map((bound, index) => ({
      transcript_id: bound.id,
      position: index,
      previous_visibility: "private",
      title: bound.title,
      session_start: "2026-01-01T00:00:00Z",
    })),
    confirmStatus: row.confirm_status ?? undefined,
    confirmMessage: "This pull request is not in a state that allows that action",
    transcriptReads: row.transcript_reads.map((read) => ({
      id: read.id,
      title: read.title,
      visibility: read.visibility,
      collectives: read.collectives,
      status: read.status ?? undefined,
    })),
  };
}

/** Text as a reader sees it: runs of whitespace are one space. */
function spoken(node: Element | null): string {
  return (node?.textContent ?? "").replace(/\s+/g, " ").trim();
}

/**
 * Mount one case and wait until the page has said everything it will say: the
 * title is up and, for the author, every transcript read has answered, so the
 * audience line is in its final form rather than its loading fallback.
 */
async function mountSettled(row: PullRequestPageCase) {
  const requests = installPullRequestREST(fixtureFor(row));
  await renderPullRequestRoute(OWNER, NAME, NUMBER);
  await screen.findByTestId("pull-request-title");
  await waitFor(() => {
    const lines = screen.queryAllByTestId("transcript-audience").map((node) => spoken(node));
    expect(lines).toEqual(row.expect_audience_lines);
    const pending = screen.queryByTestId("audience-pending") !== null;
    expect(pending).toBe(row.expect_audience_form === "fallback");
  });
  if (row.expect_audience_form === "fallback") {
    // The fallback is only a verdict once every read has answered, so wait for
    // the failed read to have been asked rather than catching the page mid-load.
    await waitFor(() => {
      for (const read of row.transcript_reads) {
        expect(requests.some((r) => r.method === "GET" && r.url.endsWith(`/transcripts/${read.id}`))).toBe(true);
      }
    });
  }
  return requests;
}

function splitLabels(): string[] {
  return [...document.querySelectorAll('[role="option"] .pd-split-option-label')].map((node) =>
    spoken(node),
  );
}

describe("the pull request page", () => {
  for (const row of loadPullRequestPageFixtures()) {
    it(`renders ${row.name}`, async () => {
      await mountSettled(row);

      expect(spoken(screen.getByTestId("pull-request-title"))).toBe(row.expect_title);
      expect(spoken(screen.queryByTestId("pull-request-sub")) || null).toBe(row.expect_sub);

      // The author's plain state line, and nothing of the kind for anyone else.
      const stateLine = screen.queryByTestId("attachment-state-line");
      expect(stateLine === null ? null : spoken(stateLine)).toBe(row.expect_state_line);

      // Exactly the controls the state and the viewer allow.
      const present = PULL_REQUEST_CONTROLS.filter((id) => screen.queryByTestId(id) !== null);
      expect([...present].sort()).toEqual([...row.expect_controls].sort());
      const confirm = screen.queryByTestId("confirm-attachment");
      expect(confirm === null ? null : spoken(confirm)).toBe(row.expect_confirm_label);

      // Who can read the transcripts: one sentence when every transcript has
      // the same audience, one line per transcript when they differ, and no
      // description at all while a read has not answered.
      const audienceText = spoken(screen.queryByTestId("pull-request-actions"));
      if (row.expect_audience_form === "sentence") {
        expect(audienceText).toMatch(/who can read (it|them): .+\. attaching does not change that\./);
      } else if (row.expect_audience_form === "list") {
        expect(audienceText).toContain("who can read them (attaching does not change that):");
        const listed = row.transcript_reads.map((read, index) => `${read.title}: ${row.expect_audience_lines[index]}`);
        for (const line of listed) expect(audienceText).toContain(line);
      } else if (row.expect_audience_form === "fallback") {
        expect(audienceText).toContain("attaching does not change who can read them.");
      } else {
        expect(audienceText).not.toContain("who can read");
        expect(audienceText).not.toContain("attaching does not change");
      }

      expect(spoken(screen.queryByTestId("pull-request-coverage")) || null).toBe(row.expect_coverage);

      if (row.expect_digest_block === "split") {
        await waitFor(() => expect(document.querySelector(".pd.pd-layout-split")).toBeTruthy());
        expect(splitLabels()).toEqual(row.expect_split_labels);
        // The first transcript is selected, and its prompts are what the pane shows.
        const options = screen.getAllByRole("option");
        expect(options[0].getAttribute("aria-selected")).toBe("true");
        expect(screen.getByText("please add the picker")).toBeTruthy();
      } else {
        expect(document.querySelector(".pd")).toBeNull();
        expect(screen.queryByTestId("digest-empty") !== null).toBe(row.expect_digest_block === "empty");
        expect(screen.queryByTestId("digest-absent") !== null).toBe(row.expect_digest_block === "absent");
      }

      // The attach-state list and its per-row marks are gone: the split list
      // is the one list of transcripts on this page.
      expect(screen.queryByLabelText("attached transcripts")).toBeNull();
      expect(document.body.textContent ?? "").not.toContain("visibility before attach");
      expect(document.body.textContent ?? "").not.toContain("not available");
    });
  }

  it("never says attaching widens who can read a transcript", async () => {
    // A transcript's own audience ("anyone" for a public one) is stated inside
    // its audience line; that is the only place such words may appear. Every
    // other word on the page is scanned for a claim that attaching opens,
    // shares or widens access.
    const forbidden = [
      /readable by/i,
      /makes the transcripts/i,
      /collaborators/i,
      /\banyone\b/i,
      /attaching (makes|opens|shares|widens|publishes|grants)/i,
    ];
    let statedAudiences = 0;
    let saidAttachingChangesNothing = 0;
    for (const row of loadPullRequestPageFixtures()) {
      await mountSettled(row);
      const page = document.body.cloneNode(true) as HTMLElement;
      for (const line of page.querySelectorAll('[data-testid="transcript-audience"]')) {
        if (/\banyone\b/.test(line.textContent ?? "")) statedAudiences++;
        line.remove();
      }
      const text = spoken(page);
      if (text.includes("attaching does not change")) saidAttachingChangesNothing++;
      for (const pattern of forbidden) {
        expect(text, `${row.name} says ${pattern}`).not.toMatch(pattern);
      }
      cleanup();
    }
    // Non-vacuous: the scan ran over a page that states a public transcript's
    // audience (so removing audience lines mattered) and over pages that make
    // the no-change statement.
    expect(statedAudiences).toBeGreaterThan(0);
    expect(saidAttachingChangesNothing).toBeGreaterThan(0);
  });

  it("links back to the collectives route when there is no attachment here", async () => {
    const row = pullRequestPageCase("reader of an attached digest");
    installPullRequestREST({ ...fixtureFor(row), attachmentStatus: 404 });
    await renderPullRequestRoute(OWNER, NAME, NUMBER);

    await screen.findByTestId("pull-request-page-not-found");
    const back = screen.getByTestId("pull-request-back-link");
    expect(back.getAttribute("href")).toBe("/groups");
    expect(spoken(back)).toBe("back to collectives");
  });

  it("shows the server's message when a confirm is refused with a conflict", async () => {
    await mountSettled(pullRequestPageCase("confirm refused with a conflict"));

    fireEvent.click(screen.getByTestId("confirm-attachment"));

    await waitFor(() => {
      expect(spoken(screen.getByTestId("attachment-action-error"))).toContain(
        "This pull request is not in a state that allows that action",
      );
    });
  });

  it("attaches the matching transcripts with the attach button", async () => {
    const requests = await mountSettled(pullRequestPageCase("author previewing two transcripts with one audience"));

    fireEvent.click(screen.getByTestId("confirm-attachment"));

    await waitFor(() => {
      expect(requests.some((r) => r.method === "POST" && r.url.endsWith(`/pulls/${OWNER}/${NAME}/${NUMBER}/confirm`))).toBe(true);
    });
    await waitFor(() => {
      expect(spoken(screen.getByTestId("attachment-state-line"))).toBe(
        "2 of your transcripts are attached to this pull request.",
      );
    });
    expect(screen.getByTestId("detach-attachment")).toBeTruthy();
  });

  it("declines a preview with not now through the detach route", async () => {
    const requests = await mountSettled(pullRequestPageCase("author previewing two transcripts with one audience"));

    fireEvent.click(screen.getByTestId("not-now-attachment"));

    await waitFor(() => {
      expect(requests.some((r) => r.method === "DELETE" && r.url.endsWith(`/pulls/${OWNER}/${NAME}/${NUMBER}`))).toBe(true);
    });
    // The response the server returned is what the page now shows, so the state
    // it renders can never be one the server has moved past.
    await waitFor(() => {
      expect(screen.getByTestId("attachment-state-line").getAttribute("data-state")).toBe("detached");
    });
    expect(spoken(screen.getByTestId("attachment-state-line"))).toContain(
      "you detached your transcripts from this pull request.",
    );
    expect(screen.queryByTestId("confirm-attachment")).toBeNull();
    expect(screen.queryByTestId("not-now-attachment")).toBeNull();
  });

  it("replaces the cached attachment with the server's answer on detach", async () => {
    const requests = await mountSettled(pullRequestPageCase("author viewing an attached digest"));

    fireEvent.click(screen.getByTestId("detach-attachment"));

    await waitFor(() => {
      expect(requests.some((r) => r.method === "DELETE")).toBe(true);
    });
    await waitFor(() => {
      expect(screen.getByTestId("attachment-state-line").getAttribute("data-state")).toBe("detached");
    });
    expect(screen.queryByTestId("detach-attachment")).toBeNull();
  });

  it("moves through the transcripts with j and k", async () => {
    await mountSettled(pullRequestPageCase("reader moving through two transcripts"));
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(2));

    const selectedLabel = () =>
      spoken(document.querySelector('[role="option"][aria-selected="true"] .pd-split-option-label'));
    const paneTitle = () => spoken(document.querySelector(".pd-split-pane-title"));

    expect(selectedLabel()).toBe("Fix flaky ingest test");
    expect(paneTitle()).toBe("Fix flaky ingest test");

    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => expect(selectedLabel()).toBe("Guard empty turns in the digest"));
    expect(paneTitle()).toBe("Guard empty turns in the digest");
    expect(screen.getByText("prompt for 22222222-2222-2222-2222-222222222222")).toBeTruthy();

    // The list stops at its end rather than wrapping.
    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => expect(selectedLabel()).toBe("Guard empty turns in the digest"));

    fireEvent.keyDown(window, { key: "k" });
    await waitFor(() => expect(selectedLabel()).toBe("Fix flaky ingest test"));
    expect(screen.getByText("please add the picker")).toBeTruthy();
  });

  it("links each chain item where it belongs", async () => {
    await mountSettled(pullRequestPageCase("reader of an attached digest"));
    await waitFor(() => expect(document.querySelector(".pd")).toBeTruthy());

    // The pane opens the whole transcript on village.
    const open = [...document.querySelectorAll(".pd a")].find((a) => spoken(a) === "open the transcript");
    expect(open?.getAttribute("href")).toBe("/transcripts/11111111-1111-1111-1111-111111111111");
    // A prompt opens its exact turn.
    const turnLinks = [...document.querySelectorAll('.pd a[href*="?turn="]')].map((a) =>
      a.getAttribute("href"),
    );
    expect(turnLinks).toContain("/transcripts/11111111-1111-1111-1111-111111111111?turn=0");
    // The commit anchor is the only chain link that leaves village, and it
    // resolves through the attachment's repository.
    const commit = document.querySelector('.pd a[href*="/commit/"]');
    expect(commit?.getAttribute("href")).toBe(
      `https://github.com/${OWNER}/${NAME}/commit/abc1234000000000000000000000000000000001`,
    );
    // The pull request itself opens on GitHub.
    const github = [...document.querySelectorAll("a")].find((a) => spoken(a) === "view on github");
    expect(github?.getAttribute("href")).toBe(`https://github.com/${OWNER}/${NAME}/pull/${NUMBER}`);
  });
});
