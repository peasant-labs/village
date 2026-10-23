import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  PULL_REQUEST_CONTROLS,
  loadPullRequestPageFixtures,
  loadForbiddenAttachmentClaims,
  loadPullRequestItemLinks,
  pullRequestPageCase,
  type PullRequestPageCase,
} from "@/test/pullRequestPageFixtures";
import {
  installAttachmentSurfacesTeardown,
  installPullRequestREST,
  makeDigest,
  renderPullRequestRoute,
  renderSignedOutPullRequestRoute,
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
    detachStatus: row.detach_status ?? undefined,
    detachMessage: row.expect_error ?? undefined,
    attachmentStatus: row.attachment_status ?? undefined,
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
      if (row.expect_route_error) {
        const requests = installPullRequestREST(fixtureFor(row));
        await renderPullRequestRoute(OWNER, NAME, row.route_number);
        await screen.findByText(row.expect_route_error);
        expect(document.querySelector('.pd')).toBeNull();
        if (row.route_number === "wrong") expect(requests).toHaveLength(0);
        return;
      }
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
        expect(audienceText).toContain(row.expect_audience_sentence);
      } else if (row.expect_audience_form === "list") {
        // The band says each row carries its own audience, and each row does:
        // the row named by the transcript's title (or its session label) says
        // who can read that transcript.
        expect(audienceText).toContain("who can read each one is on its row. attaching does not change that.");
        const shownRows = screen.getAllByTestId("pull-request-transcript");
        row.transcript_reads.forEach((read, index) => {
          const shown = shownRows.find((node) => node.getAttribute("data-transcript-id") === read.id);
          expect(spoken(shown?.querySelector(".is-title") ?? null)).toBe(
            read.title?.trim() ? read.title : `session ${index + 1}`,
          );
          expect(spoken(shown?.querySelector(".on-pr-audience") ?? null)).toBe(
            `who can read it: ${row.expect_audience_lines[index]}`,
          );
        });
      } else if (row.expect_audience_form === "fallback") {
        expect(audienceText).toContain("attaching does not change who can read them.");
      } else {
        expect(audienceText).not.toContain("who can read");
        expect(audienceText).not.toContain("attaching does not change");
      }

      // Only the list form puts an audience on a row; every other form says it
      // once, or not at all.
      if (row.expect_audience_form !== "list") {
        expect(document.querySelector(".on-pr-audience")).toBeNull();
      }

      expect(spoken(screen.queryByTestId("pull-request-coverage")) || null).toBe(row.expect_coverage);

      // The repository line carries the attachment's own state, in its own word.
      expect(spoken(screen.getByTestId("pull-request-state"))).toBe(row.state);

      // The card lists one row per session the split list offers, in the same
      // order and by the same names, and no rows when there is no digest to list.
      const rowLabels = screen
        .queryAllByTestId("pull-request-transcript")
        .map((node) => spoken(node.querySelector(".is-title")));
      expect(rowLabels).toEqual(row.expect_digest_block === "split" ? row.expect_split_labels : []);

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
        expect(spoken(screen.getByTestId(`digest-${row.expect_digest_block}`))).toBe(row.expect_digest_text);
      }

      if (row.action) {
        fireEvent.click(screen.getByTestId(row.action));
        await waitFor(() => expect(spoken(screen.getByTestId("attachment-action-error"))).toBe(row.expect_error));
        expect(screen.getByTestId("attachment-state-line").getAttribute("data-state")).toBe(row.state);
        expect(screen.getByTestId(row.action)).toBeTruthy();
      }

      // The old attach-state list and its per-row marks stay gone: the card's
      // rows name the same sessions the split list does, and nothing else.
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
    const forbidden = loadForbiddenAttachmentClaims();
    let statedAudiences = 0;
    let saidAttachingChangesNothing = 0;
    for (const row of loadPullRequestPageFixtures().filter(row => !row.expect_route_error)) {
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
    expect(back.getAttribute("href")).toBe("/collectives");
    expect(spoken(back)).toBe("back to collectives");
    // Nobody is asked to sign in before the page knows they are signed out.
    expect(screen.queryByTestId("pull-request-sign-in")).toBeNull();
  });

  it("offers a signed-out reader sign-in back to this same pull request", async () => {
    // A private repository's attachment opens to its readers once they sign
    // in, so the one refusal offers that, and returns to this page after.
    installPullRequestREST({ ...fixtureFor(pullRequestPageCase("missing attachment")) });
    await renderSignedOutPullRequestRoute(OWNER, NAME, NUMBER);

    const signIn = await screen.findByTestId("pull-request-sign-in");
    expect(spoken(signIn)).toBe("sign in");
    expect(signIn.getAttribute("href")).toBe(
      `/login?next=${encodeURIComponent(`/pulls/${OWNER}/${NAME}/${NUMBER}`)}`,
    );
    expect(screen.getByTestId("pull-request-back-link").getAttribute("href")).toBe("/collectives");
  });

  it("reads the attachment again from the failure's retry", async () => {
    const requests = installPullRequestREST({
      ...fixtureFor(pullRequestPageCase("attachment read fails")),
      attachmentFailsOnce: true,
    });
    await renderPullRequestRoute(OWNER, NAME, NUMBER);
    await screen.findByText("this pull request page could not be loaded");

    fireEvent.click(screen.getByTestId("pull-request-retry"));

    await screen.findByTestId("pull-request-title");
    const reads = requests.filter(
      (r) => r.method === "GET" && r.url.endsWith(`/pulls/${OWNER}/${NAME}/${NUMBER}`),
    );
    expect(reads).toHaveLength(2);
  });

  it("names who ran each of the author's sessions, its agent and its turns", async () => {
    // The author's page already reads each matching transcript to say who can
    // read it, so its rows also carry what those reads say about the session.
    await mountSettled(pullRequestPageCase("author previewing two transcripts with one audience"));
    const [first] = screen.getAllByTestId("pull-request-transcript");
    expect(spoken(first.querySelector(".on-pr-rowmeta"))).toMatch(
      /^@alice-dev, claude-code, 2 turns, 1 commit, \S/,
    );
    // The title opens the whole transcript from this pull request.
    expect(first.querySelector(".is-title")?.getAttribute("href")).toBe(
      `/transcripts/11111111-1111-1111-1111-111111111111?in=pull:${OWNER}/${NAME}/${NUMBER}`,
    );
  });

  it("tells a reader only what the digest itself says about each session", async () => {
    // A reader's page makes no transcript reads of its own, so a row says the
    // digest's counts and when the session started, and nothing about its owner.
    await mountSettled(pullRequestPageCase("reader of an attached digest"));
    const [first] = screen.getAllByTestId("pull-request-transcript");
    const meta = spoken(first.querySelector(".on-pr-rowmeta"));
    expect(meta).toMatch(/^1 commit, \S/);
    expect(meta).not.toContain("@");
    expect(meta).not.toContain("turn");
  });

  it("selects a row's session in the split list from the row's prompts control", async () => {
    await mountSettled(pullRequestPageCase("reader moving through two transcripts"));
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(2));
    const jumps = screen.getAllByTestId("pull-request-jump");
    expect(jumps.map((jump) => spoken(jump))).toEqual(["read 2 prompts", "read 2 prompts"]);

    fireEvent.click(jumps[1]);

    await waitFor(() =>
      expect(
        spoken(document.querySelector('[role="option"][aria-selected="true"] .pd-split-option-label')),
      ).toBe("Guard empty turns in the digest"),
    );
    // Focus lands on the list, so j, k and the arrow keys carry on from there.
    expect(document.activeElement?.classList.contains("pd-split-list")).toBe(true);
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

  it("declines a preview through the detach route, and says it does not attach", async () => {
    const requests = await mountSettled(pullRequestPageCase("author previewing two transcripts with one audience"));
    // Declining detaches, so the control says what it does rather than "not
    // now", which read as if the question would come back by itself.
    expect(spoken(screen.getByTestId("not-now-attachment"))).toBe("don't attach");

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

  for (const row of loadPullRequestItemLinks()) {
    it(row.name, async () => {
      const fixture = fixtureFor(pullRequestPageCase("reader of an attached digest"));
      const item = fixture.digest!.items.find(item => item.kind === row.kind)!;
      if (row.turnIndex !== null) item.turnIndex = row.turnIndex;
      if (row.commitSha !== null) item.commitSha = row.commitSha;
      installPullRequestREST(fixture);
      await renderPullRequestRoute(OWNER, NAME, NUMBER);
      await screen.findByTestId("pull-request-title");
      await waitFor(() => expect(document.querySelector(`.pd a[href="${row.expected}"]`)).toBeTruthy());
    });
  }

  it("links each chain item where it belongs", async () => {
    await mountSettled(pullRequestPageCase("reader of an attached digest"));
    await waitFor(() => expect(document.querySelector(".pd")).toBeTruthy());

    // The pane opens the whole transcript on village.
    const open = [...document.querySelectorAll(".pd a")].find((a) => spoken(a) === "open the transcript");
    expect(open?.getAttribute("href")).toBe(
      `/transcripts/11111111-1111-1111-1111-111111111111?in=pull:${OWNER}/${NAME}/${NUMBER}`,
    );
    // A prompt opens its exact turn, from this pull request.
    const turnLinks = [...document.querySelectorAll('.pd a[href*="?turn="]')].map((a) =>
      a.getAttribute("href"),
    );
    expect(turnLinks).toContain(
      `/transcripts/11111111-1111-1111-1111-111111111111?turn=0&in=pull:${OWNER}/${NAME}/${NUMBER}`,
    );
    expect(turnLinks).toContain(
      `/transcripts/11111111-1111-1111-1111-111111111111?turn=1&in=pull:${OWNER}/${NAME}/${NUMBER}`,
    );
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
