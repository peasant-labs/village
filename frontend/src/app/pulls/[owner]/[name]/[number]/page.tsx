"use client";

import { use, useId, useMemo, useRef, useState, type ReactNode } from "react";
import Link from "next/link";
import { useQueries } from "@tanstack/react-query";
import { AlertCircle, ArrowDown, ExternalLink, GitBranch, GitPullRequest, Lock } from "lucide-react";
import { Button, PromptDigest, ProviderName } from "@/lib/ft-ui";
import { OnEmpty, OnPage } from "@/components/online/Page";
import PullStateTag from "@/components/pulls/PullStateTag";
import RetryButton from "@/components/RetryButton";
import { isApiErrorStatus } from "@/lib/api";
import { formatRelativeTime } from "@/lib/format";
import { isHarness } from "@/lib/harness";
import { loginHref } from "@/lib/signIn";
import {
  pullPath,
  useConfirmPullRequestAttachment,
  useDetachPullRequestAttachment,
  usePullRequestAttachment,
} from "@/lib/queries/pulls";
import { transcriptQueryOptions } from "@/lib/queries/transcripts";
import { transcriptCollectivesQueryOptions } from "@/lib/queries/collectives";
import { useAuth } from "@/providers/AuthProvider";
import type {
  PromptDigest as PromptDigestPayload,
  PromptDigestItem,
  VillagePullRequestAttachmentState,
} from "@peasant-labs/schema";
import type { TranscriptCollective, TranscriptDetailResponse } from "@/lib/types";
import "@/app/online-pulls.css";

/**
 * The pull request page: `/pulls/{owner}/{name}/{number}`.
 *
 * It is where the pull request's comment and check lead, and it reads signed
 * out as well as signed in: a public repository's attachment is anyone's to
 * read. The pull request is one card (its repository, title and branch, the
 * transcripts behind it as rows, and the way back to GitHub); the prompts sit
 * under it in the design system's digest, in its split layout (the transcripts
 * on the left, the selected one's prompts on the right, `j` and `k` to move).
 * This page owns where each item links, the author's attach, decline and
 * detach, and plain words about the state the attachment is in.
 *
 * Attaching never changes who can read a transcript. The server narrows the
 * digest to what the viewer may open, and for the author the page reads each
 * matching transcript's own audience (the transcript read and its collectives
 * read) and says it back. That line restates what the server already decided;
 * it decides nothing, so when the server's rule changes, only the words move.
 *
 * The wire carries the repository owner and name but not its host, and GitHub is
 * the only provider peasant connects today, so a commit anchor and the "view on
 * github" link go to GitHub. If another host is ever supported, that becomes a
 * contract question rather than a second string built here.
 */
const GITHUB_HOST = "https://github.com";

/** One chain item's destination: the turn, the transcript, or the commit. */
function itemHref(item: PromptDigestItem, owner: string, name: string): string {
  if (item.kind === "commit") {
    return `${GITHUB_HOST}/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/commit/${encodeURIComponent(item.commitSha ?? item.text)}`;
  }
  if ((item.kind === "prompt" || item.kind === "skill") && item.turnIndex != null) {
    return `/transcripts/${item.transcriptId}?turn=${item.turnIndex}`;
  }
  return `/transcripts/${item.transcriptId}`;
}

/** The transcripts a digest names, once each, in chain order. */
function digestTranscriptIds(digest: PromptDigestPayload | null | undefined): string[] {
  if (!digest) return [];
  const seen = new Set<string>();
  const ids: string[] = [];
  for (const item of digest.items) {
    if (item.kind === "session" && !seen.has(item.transcriptId)) {
      seen.add(item.transcriptId);
      ids.push(item.transcriptId);
    }
  }
  return ids;
}

/** "A", "A and B", "A, B and C": collective names as they are written. */
function joinNames(names: string[]): string {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

/**
 * Who can read one transcript, in the words the page uses: everyone for a
 * public transcript, the members of the collectives it was published to, or
 * only its owner. It restates the transcript's own audience, which attaching
 * does not change.
 */
function audienceOf(visibility: string, collectiveNames: string[]): string {
  if (visibility === "public") return "anyone";
  // Approval metadata can remain after a transcript becomes private. It does
  // not grant ordinary members access, and an empty list cannot rule out the
  // collective owners' review access to pending submissions.
  if (visibility === "private") return "not shared with collective members";
  if (collectiveNames.length > 0) return `members of ${joinNames(collectiveNames)}`;
  return "not shared with collective members";
}

function transcriptsNoun(count: number): string {
  return count === 1 ? "transcript" : "transcripts";
}

function plural(count: number, one: string, many: string): string {
  return count === 1 ? one : many;
}

/** A session's length: "9m", "1h 16m", or "under a minute". */
function formatSessionLength(ms: number): string {
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 1) return "under a minute";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest > 0 ? `${hours}h ${String(rest).padStart(2, "0")}m` : `${hours}h`;
}

/** How long a transcript's session ran, when its own read says. */
function sessionLength(detail: TranscriptDetailResponse): number | null {
  const t = detail.transcript;
  if (t.duration_ms != null && t.duration_ms > 0) return t.duration_ms;
  if (t.session_start && t.session_end) {
    const ms = Date.parse(t.session_end) - Date.parse(t.session_start);
    return Number.isFinite(ms) && ms > 0 ? ms : null;
  }
  return null;
}

/** One matching transcript's audience, once its two reads have answered. */
interface TranscriptAudience {
  transcriptId: string;
  title: string | null;
  audience: string;
}

/** One transcript row on the card: what the digest says about one session. */
interface TranscriptRowData {
  transcriptId: string;
  label: string;
  promptCount: number | null;
  commitCount: number | null;
  startedAt: string;
  /** Where this session sits in the digest's split list. */
  splitIndex: number;
}

/**
 * The rows the card lists: one per session the digest names, in its order, with
 * the split-list position each one is selected by. A chain that opens before
 * its first session gets a leading entry in the split list, so the positions
 * shift by one.
 */
function transcriptRows(digest: PromptDigestPayload | null): TranscriptRowData[] {
  if (!digest) return [];
  const leading = digest.items.length > 0 && digest.items[0].kind !== "session" ? 1 : 0;
  const rows: TranscriptRowData[] = [];
  for (const item of digest.items) {
    if (item.kind !== "session") continue;
    rows.push({
      transcriptId: item.transcriptId,
      label: item.text,
      promptCount: item.promptCount ?? null,
      commitCount: item.commitCount ?? null,
      startedAt: item.timestamp,
      splitIndex: rows.length + leading,
    });
  }
  return rows;
}

export default function PullRequestPage({
  params,
}: {
  params: Promise<{ owner: string; name: string; number: string }>;
}) {
  const { owner, name, number } = use(params);
  const pullNumber = Number(number);
  const attachment = usePullRequestAttachment(owner, name, pullNumber);
  const confirm = useConfirmPullRequestAttachment(owner, name, pullNumber);
  const detach = useDetachPullRequestAttachment(owner, name, pullNumber);
  const auth = useAuth();
  const [selected, setSelected] = useState(0);
  const promptsRef = useRef<HTMLElement>(null);
  const titleId = useId();
  const promptsTitleId = useId();

  const data = attachment.data;
  const digest = data?.digest ?? null;
  const state = data?.attachment.state;
  const viewerIsAuthor = data?.viewer_is_author ?? false;
  const sessionIds = useMemo(() => digestTranscriptIds(digest), [digest]);

  // The author is told who can read what they attach, from each transcript's own
  // reads. Nobody else is: a reader sees only the transcripts they can open, and
  // the audience of those is the transcript page's to state.
  const readsAudience = viewerIsAuthor && (state === "preview" || state === "attached");
  const audienceIds = readsAudience ? sessionIds : [];
  const reads = useQueries({
    queries: audienceIds.flatMap((id) => [
      transcriptQueryOptions(id),
      transcriptCollectivesQueryOptions(id),
    ]),
  });
  // Every read must have answered before anything is said: a row still loading,
  // or one that failed, is never described by a guess.
  const audiences: TranscriptAudience[] | null =
    audienceIds.length > 0 && reads.every((read) => read.isSuccess)
      ? audienceIds.map((transcriptId, index) => {
          const detail = reads[index * 2].data as TranscriptDetailResponse;
          const collectives = reads[index * 2 + 1].data as TranscriptCollective[];
          return {
            transcriptId,
            title: detail.transcript.title,
            audience: audienceOf(
              detail.transcript.visibility,
              collectives.map((collective) => collective.name),
            ),
          };
        })
      : null;
  const sharedAudience =
    audiences && audiences.length > 0 && audiences.every((row) => row.audience === audiences[0].audience)
      ? audiences[0].audience
      : null;
  // When the transcripts' audiences differ, each row says its own.
  const rowAudience = new Map<string, string>(
    audiences && sharedAudience === null
      ? audiences.map((row) => [row.transcriptId, row.audience] as const)
      : [],
  );

  // The split list names each transcript by its title where the page knows it:
  // the attachment's own list carries the titles this viewer may read, and the
  // author's reads carry theirs. A session the page cannot name keeps the
  // digest's own label. The query data itself is never changed.
  const titleKey = JSON.stringify([
    ...(data?.transcripts ?? []).map((row) => [row.transcript_id, row.title ?? ""]),
    ...(audiences ?? []).map((row) => [row.transcriptId, row.title ?? ""]),
  ]);
  const { titledDigest, rows } = useMemo(() => {
    if (!digest) return { titledDigest: null, rows: [] };
    const titles = new Map<string, string>();
    for (const [id, title] of JSON.parse(titleKey) as [string, string][]) {
      if (title.trim() !== "" && !titles.has(id)) titles.set(id, title);
    }
    const titled = {
      ...digest,
      items: digest.items.map((item) => {
        const title = item.kind === "session" ? titles.get(item.transcriptId) : undefined;
        return title ? { ...item, text: title } : item;
      }),
    };
    // The card's rows are the same sessions under the same names.
    return { titledDigest: titled, rows: transcriptRows(titled) };
  }, [digest, titleKey]);
  // What the author's own reads say about each session, as each one answers.
  const details = new Map<string, TranscriptDetailResponse>(
    audienceIds.flatMap((id, index) => {
      const read = reads[index * 2];
      return read?.isSuccess && read.data ? [[id, read.data as TranscriptDetailResponse] as const] : [];
    }),
  );

  // A number that is not a positive integer cannot address an attachment, so the
  // page answers the same way it answers one that does not exist rather than
  // waiting forever on a query that is never enabled.
  const addressable = Number.isInteger(pullNumber) && pullNumber > 0;

  // ONE refusal for every case where no attachment is readable here: a missing
  // pull request, a private repository's attachment the caller may not see, and
  // an unlinked repository are deliberately indistinguishable.
  if (!addressable || isApiErrorStatus(attachment.error, 404)) {
    // A private repository's attachment opens to its readers once they sign in,
    // so somebody reading signed out is offered that, back to this same page.
    const signedOut = !auth.isLoading && !auth.isLoggedIn;
    const here = addressable ? pullPath(owner, name, pullNumber) : null;
    return (
      <OnPage tight testId="pull-request-page-not-found">
        <OnEmpty
          icon={<GitPullRequest aria-hidden="true" />}
          title="no attachment here"
          body={
            signedOut
              ? "this pull request has no prompts on peasant that you can open signed out."
              : "this pull request has no prompts on peasant that you can open."
          }
          actions={
            <>
              {signedOut && (
                <Link
                  href={loginHref(here)}
                  className="btn btn-primary"
                  data-testid="pull-request-sign-in"
                >
                  sign in
                </Link>
              )}
              <Link
                href="/collectives"
                data-testid="pull-request-back-link"
                className="btn btn-secondary"
              >
                back to collectives
              </Link>
            </>
          }
        />
      </OnPage>
    );
  }

  if (attachment.error) {
    return (
      <OnPage tight testId="pull-request-page-error">
        <div className="on-notice" data-tone="danger">
          <div role="alert" className="on-pr-fail">
            <p className="on-pr-fail-title">this pull request page could not be loaded</p>
            <p>
              The request to peasant online failed before the attachment could be read, so nothing
              is shown rather than part of the page.
            </p>
          </div>
          <RetryButton
            label={attachment.isFetching ? "retrying" : "retry"}
            busy={attachment.isFetching}
            onRetry={() => attachment.refetch()}
            testId="pull-request-retry"
          />
        </div>
      </OnPage>
    );
  }

  if (attachment.isLoading || !data || !state) {
    return (
      <OnPage tight testId="pull-request-page-pending">
        <div className="on-prcard" aria-busy="true">
          <header>
            <div className="h-4 w-40 animate-shimmer" />
            <div className="h-8 w-2/3 animate-shimmer" />
            <div className="h-4 w-56 animate-shimmer" />
          </header>
        </div>
        <div className="h-64 w-full animate-shimmer" />
      </OnPage>
    );
  }

  const repoLabel = `${data.attachment.owner}/${data.attachment.name}`;
  const pullTitle = data.attachment.title?.trim() ? data.attachment.title : null;
  const githubHref = `${GITHUB_HOST}/${encodeURIComponent(data.attachment.owner)}/${encodeURIComponent(data.attachment.name)}/pull/${data.attachment.number}`;

  // Bound transcripts the digest does not carry are ones this viewer cannot
  // open. They are counted, never named, the same way the pull request's own
  // comment used to count them.
  const digestIds = new Set(digest?.items.map((item) => item.transcriptId) ?? []);
  const unlisted = digest
    ? data.transcripts.filter((row) => !digestIds.has(row.transcript_id)).length
    : 0;

  const hasSplit = !!titledDigest && titledDigest.items.length > 0;

  const pending = confirm.isPending || detach.isPending;
  const actionError = confirm.error ?? detach.error;
  const onAttach = () => {
    detach.reset();
    confirm.mutate();
  };
  const onDetach = () => {
    confirm.reset();
    detach.mutate();
  };
  const hasActions =
    viewerIsAuthor && (state === "preview" || state === "attached" || state === "waiting");
  const actions = hasActions ? (
    <AuthorActions
      state={state}
      matching={digest ? digest.header.sessionCount : 0}
      pending={pending}
      onAttach={onAttach}
      onDetach={onDetach}
    />
  ) : null;

  // Selecting a row's session in the digest below, and taking focus there so
  // `j`, `k` and the arrow keys carry on from it.
  const jumpTo = (index: number) => {
    setSelected(index);
    const section = promptsRef.current;
    if (!section) return;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    section.scrollIntoView?.({ block: "start", behavior: reduce ? "auto" : "smooth" });
    section.querySelector<HTMLElement>(".pd-split-list")?.focus({ preventScroll: true });
  };

  return (
    <OnPage tight testId="pull-request-page">
      <article className="on-prcard on-pr-card" aria-labelledby={titleId}>
        <header>
          <div className="on-prcard-repo">
            <GitPullRequest aria-hidden="true" />
            <span className="on-pr-repo-name">{repoLabel}</span>{" "}
            <span className="on-pr-num">#{data.attachment.number}</span>
            <span className="on-prcard-chips">
              <PullStateTag state={state} testId="pull-request-state" />
              {data.attachment.is_private_repository && (
                <span className="on-tag">
                  <Lock aria-hidden="true" />
                  private repository
                </span>
              )}
            </span>
          </div>
          <h1 className="on-pr-title" id={titleId} data-testid="pull-request-title">
            {pullTitle ?? `${repoLabel} #${data.attachment.number}`}
          </h1>
          {(data.attachment.head_ref || digest) && (
            <p className="on-prcard-meta" data-testid="pull-request-sub">
              {data.attachment.head_ref && (
                <span className="on-pr-ref">
                  <GitBranch aria-hidden="true" />
                  {data.attachment.head_ref}
                </span>
              )}{" "}
              {digest && (
                <span className="on-pr-n">
                  {digest.header.commitsTotal} {plural(digest.header.commitsTotal, "commit", "commits")}
                </span>
              )}
            </p>
          )}
        </header>

        {viewerIsAuthor && (
          <AuthorBand
            state={state}
            matching={digest ? digest.header.sessionCount : 0}
            showsAudience={audienceIds.length > 0}
            audiences={audiences}
            sharedAudience={sharedAudience}
          />
        )}

        <div className="on-prcard-body">
          {rows.length > 0 ? (
            <ul className="on-pr-rows" aria-label="transcripts behind this pull request">
              {rows.map((row) => (
                <TranscriptRow
                  key={`${row.splitIndex}-${row.transcriptId}`}
                  row={row}
                  detail={details.get(row.transcriptId) ?? null}
                  audience={rowAudience.get(row.transcriptId) ?? null}
                  onJump={hasSplit ? () => jumpTo(row.splitIndex) : null}
                />
              ))}
            </ul>
          ) : digest ? (
            // The digest is present but has no rows: every bound prompt is gone. Say
            // so in words rather than rendering an empty shell where a digest was,
            // and say only that: which prompt left, and why, is its owner's.
            <p className="on-pr-note" data-testid="digest-empty">
              no prompts are available for this pull request.
            </p>
          ) : (
            <p className="on-pr-note" data-testid="digest-absent">
              {state === "attached"
                ? "no prompts are shown for this pull request."
                : "the prompts appear here once the author attaches them."}
            </p>
          )}
          {digest && (
            <p className="on-pr-coverage" data-testid="pull-request-coverage">
              <span className="on-pr-n">{digest.header.commitsCovered}</span> of{" "}
              <span className="on-pr-n">{digest.header.commitsTotal}</span> commits traced
              {unlisted > 0 && (
                <>
                  .{" "}
                  <span className="on-pr-n">{unlisted}</span> attached {transcriptsNoun(unlisted)}{" "}
                  {unlisted === 1 ? "is" : "are"} not listed here.
                </>
              )}
            </p>
          )}
        </div>

        <footer>
          <a className="on-pr-gh" href={githubHref} target="_blank" rel="noreferrer">
            view on github
            <ExternalLink aria-hidden="true" />
          </a>
          {(actions || actionError) && (
            <div className="on-pr-acts">
              {actionError && (
                <p className="on-pr-error" role="alert" data-testid="attachment-action-error">
                  <AlertCircle aria-hidden="true" />
                  <span>{actionError.message}</span>
                </p>
              )}
              {actions}
            </div>
          )}
        </footer>
      </article>

      {hasSplit && (
        <section className="on-pr-prompts" ref={promptsRef} aria-labelledby={promptsTitleId}>
          <h2 className="on-h2" id={promptsTitleId}>
            prompts
          </h2>
          <PromptDigest
            digest={titledDigest}
            layout="split"
            selected={selected}
            onSelect={(index) => setSelected(index)}
            itemHref={(item) => itemHref(item, data.attachment.owner, data.attachment.name)}
            className="border border-rule"
          />
        </section>
      )}
    </OnPage>
  );
}

/**
 * One transcript behind the pull request: its title (a link to the whole
 * transcript), what the page knows about its session, and a way down to its
 * prompts. Everybody sees the digest's own counts; the author, whose own reads
 * the page already makes, also sees who ran it, the agent, its turns and its
 * length, and, when the transcripts' audiences differ, who can read this one.
 */
function TranscriptRow({
  row,
  detail,
  audience,
  onJump,
}: {
  row: TranscriptRowData;
  detail: TranscriptDetailResponse | null;
  audience: string | null;
  onJump: (() => void) | null;
}) {
  const titleId = useId();
  const parts: ReactNode[] = [];
  if (detail) {
    const handle = detail.owner?.github_username;
    if (handle) parts.push(<span key="who">@{handle}</span>);
    const harness = detail.transcript.model_provider;
    parts.push(
      isHarness(harness) ? (
        <span key="agent" className="on-agent">
          <ProviderName harness={harness} />
        </span>
      ) : (
        <span key="agent">{harness}</span>
      ),
    );
    if (detail.transcript.turn_count != null) {
      parts.push(
        <span key="turns" className="on-pr-n">
          {detail.transcript.turn_count} {plural(detail.transcript.turn_count, "turn", "turns")}
        </span>,
      );
    }
    const length = sessionLength(detail);
    if (length != null) {
      parts.push(
        <span key="length" className="on-pr-n">
          {formatSessionLength(length)}
        </span>,
      );
    }
  }
  if (row.commitCount != null) {
    parts.push(
      <span key="commits" className="on-pr-n">
        {row.commitCount} {plural(row.commitCount, "commit", "commits")}
      </span>,
    );
  }
  const started = formatRelativeTime(row.startedAt);
  if (started) parts.push(<span key="when">{started}</span>);

  return (
    <li className="on-trow2" data-testid="pull-request-transcript" data-transcript-id={row.transcriptId}>
      <span className="on-rail" aria-hidden="true" />
      <div className="on-pr-rowmain">
        <Link className="is-title" id={titleId} href={`/transcripts/${row.transcriptId}`}>
          {row.label}
        </Link>
        {parts.length > 0 && (
          <p className="on-trow2-meta on-pr-rowmeta">
            {parts.flatMap((part, index) => (index > 0 ? [", ", part] : [part]))}
          </p>
        )}
        {audience !== null && (
          <p className="on-pr-audience">
            who can read it: <span data-testid="transcript-audience">{audience}</span>
          </p>
        )}
      </div>
      {onJump && row.promptCount != null && row.promptCount > 0 && (
        <button
          type="button"
          className="btn btn-ghost btn-sm on-pr-jump"
          aria-describedby={titleId}
          onClick={onJump}
          data-testid="pull-request-jump"
        >
          read <span className="on-pr-n">{row.promptCount}</span>{" "}
          {plural(row.promptCount, "prompt", "prompts")}
          <ArrowDown aria-hidden="true" />
        </button>
      )}
    </li>
  );
}

/**
 * The author's band: one plain line about the state the attachment is in and,
 * while there is something to attach or attached, who can read it. The actions
 * it allows sit at the foot of the card, after the rows they act on.
 */
function AuthorBand({
  state,
  matching,
  showsAudience,
  audiences,
  sharedAudience,
}: {
  state: VillagePullRequestAttachmentState;
  matching: number;
  showsAudience: boolean;
  audiences: TranscriptAudience[] | null;
  sharedAudience: string | null;
}) {
  let line: ReactNode;
  switch (state) {
    case "preview":
      line = (
        <>
          <strong>
            <span className="on-pr-n">{matching}</span> of your transcripts{" "}
            {matching === 1 ? "matches" : "match"} this pull request.
          </strong>{" "}
          attach {matching === 1 ? "it so reviewers can read it" : "them so reviewers can read them"} next
          to the code.
        </>
      );
      break;
    case "attached":
      line = (
        <strong>
          <span className="on-pr-n">{matching}</span> of your transcripts{" "}
          {matching === 1 ? "is" : "are"} attached to this pull request.
        </strong>
      );
      break;
    case "waiting":
      line = (
        <>
          <strong>none of your transcripts match this pull request yet.</strong> publish the session
          behind one of its commits and it is attached.
        </>
      );
      break;
    case "requested":
      line = (
        <>
          <strong>you were asked to attach your transcripts.</strong> comment{" "}
          <code>/peasant attach</code> on the pull request to review them here.
        </>
      );
      break;
    case "detached":
      line = (
        <>
          <strong>you detached your transcripts from this pull request.</strong> comment{" "}
          <code>/peasant attach</code> on it to attach them again.
        </>
      );
      break;
  }

  return (
    <section
      className="on-pr-band"
      aria-label="your transcripts on this pull request"
      data-testid="pull-request-actions"
    >
      <p data-testid="attachment-state-line" data-state={state}>
        {line}
      </p>
      {showsAudience &&
        (audiences === null ? (
          <p data-testid="audience-pending">attaching does not change who can read them.</p>
        ) : sharedAudience !== null ? (
          <p>
            who can read {audiences.length === 1 ? "it" : "them"}:{" "}
            <span data-testid="transcript-audience">{sharedAudience}</span>. attaching does not change
            that.
          </p>
        ) : (
          <p data-testid="audience-per-row">
            who can read each one is on its row. attaching does not change that.
          </p>
        ))}
    </section>
  );
}

/**
 * The one or two actions the attachment's state allows the author.
 *
 * Declining a preview detaches it: the pull request's preview comment is
 * deleted and nothing is attached until the author asks again, so the control
 * says it does not attach rather than suggesting the question comes back later.
 */
function AuthorActions({
  state,
  matching,
  pending,
  onAttach,
  onDetach,
}: {
  state: VillagePullRequestAttachmentState;
  matching: number;
  pending: boolean;
  onAttach: () => void;
  onDetach: () => void;
}) {
  switch (state) {
    case "preview":
      return (
        <>
          <Button
            variant="ghost"
            size="sm"
            onClick={onDetach}
            disabled={pending}
            data-testid="not-now-attachment"
          >
            don&apos;t attach
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={onAttach}
            disabled={pending}
            data-testid="confirm-attachment"
          >
            attach <span className="on-pr-n">{matching}</span> {transcriptsNoun(matching)}
          </Button>
        </>
      );
    case "attached":
    case "waiting":
      return (
        <Button
          variant="secondary"
          size="sm"
          onClick={onDetach}
          disabled={pending}
          data-testid="detach-attachment"
        >
          detach
        </Button>
      );
    default:
      return null;
  }
}
