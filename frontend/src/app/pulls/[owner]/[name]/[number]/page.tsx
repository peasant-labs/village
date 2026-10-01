"use client";

import { use, useMemo, useState, type ReactNode } from "react";
import Link from "next/link";
import { useQueries } from "@tanstack/react-query";
import { ExternalLink, GitPullRequest, Layers } from "lucide-react";
import { Button, EmptyState, LinkButton, PromptDigest } from "@/lib/ft-ui";
import { isApiErrorStatus } from "@/lib/api";
import {
  useConfirmPullRequestAttachment,
  useDetachPullRequestAttachment,
  usePullRequestAttachment,
} from "@/lib/queries/pulls";
import { transcriptQueryOptions } from "@/lib/queries/transcripts";
import { transcriptCollectivesQueryOptions } from "@/lib/queries/collectives";
import type {
  PromptDigest as PromptDigestPayload,
  PromptDigestItem,
  VillagePullRequestAttachmentState,
} from "@peasant-labs/schema";
import type { TranscriptCollective, TranscriptDetailResponse } from "@/lib/types";

/**
 * The pull request page: `/pulls/{owner}/{name}/{number}`.
 *
 * It is where the pull request's comment and check lead. The digest is the
 * design system's component in its split layout (the transcripts on the left,
 * the selected one's prompts on the right, `j` and `k` to move); this page owns
 * where each item links, the author's attach, not now and detach, and plain
 * words about the state the attachment is in.
 *
 * Attaching never changes who can read a transcript. The server narrows the
 * digest to what the viewer may open, and for the author the page reads each
 * matching transcript's own audience (the transcript read and its collectives
 * read) and says it back. That line restates what the server already decided;
 * it decides nothing, so when the server's rule changes, only the words move.
 *
 * The wire carries the repository owner and name but not its host, and GitHub is
 * the only provider Village connects today, so a commit anchor and the "view on
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
  if (collectiveNames.length > 0) return `members of ${joinNames(collectiveNames)}`;
  return "only you";
}

function transcriptsNoun(count: number): string {
  return count === 1 ? "transcript" : "transcripts";
}

/** One matching transcript's audience, once its two reads have answered. */
interface TranscriptAudience {
  transcriptId: string;
  title: string | null;
  audience: string;
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
  const [selected, setSelected] = useState(0);

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

  // The split list names each transcript by its title where the page knows it:
  // the attachment's own list carries the titles this viewer may read, and the
  // author's reads carry theirs. A session the page cannot name keeps the
  // digest's own label. The query data itself is never changed.
  const titleKey = JSON.stringify([
    ...(data?.transcripts ?? []).map((row) => [row.transcript_id, row.title ?? ""]),
    ...(audiences ?? []).map((row) => [row.transcriptId, row.title ?? ""]),
  ]);
  const titledDigest = useMemo(() => {
    if (!digest) return null;
    const titles = new Map<string, string>();
    for (const [id, title] of JSON.parse(titleKey) as [string, string][]) {
      if (title.trim() !== "" && !titles.has(id)) titles.set(id, title);
    }
    return {
      ...digest,
      items: digest.items.map((item) => {
        const title = item.kind === "session" ? titles.get(item.transcriptId) : undefined;
        return title ? { ...item, text: title } : item;
      }),
    };
  }, [digest, titleKey]);

  // The design system's base stylesheet caps every <section> at its
  // documentation column and centres it. On this page a section is only
  // grouping (the author's panel, the digest and its reading pane), so the cap
  // and the centring are lifted inside the page, exactly as the design
  // system's own in-use pages lift them, and each section fills its column.
  const shell =
    "max-w-[1184px] mx-auto px-4 pt-6 pb-10 flex flex-col gap-[var(--sp-5)] animate-fade-up min-w-0 [&_section]:max-w-none [&_section]:mx-0";

  // A number that is not a positive integer cannot address an attachment, so the
  // page answers the same way it answers one that does not exist rather than
  // waiting forever on a query that is never enabled.
  const addressable = Number.isInteger(pullNumber) && pullNumber > 0;

  // ONE refusal for every case where no attachment is readable here: a missing
  // pull request, a private repository's attachment the caller may not see, and
  // an unlinked repository are deliberately indistinguishable.
  if (!addressable || isApiErrorStatus(attachment.error, 404)) {
    return (
      <div className={shell}>
        <div data-testid="pull-request-page-not-found">
          <EmptyState
            icon={Layers}
            as="h3"
            title="no attachment here"
            message="village has no prompt attachment for this pull request that you can open."
            action={
              <Link
                href="/groups"
                data-testid="pull-request-back-link"
                className="font-mono text-[length:var(--fs-label)] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
              >
                back to collectives
              </Link>
            }
          />
        </div>
      </div>
    );
  }

  if (attachment.error) {
    return (
      <div className={shell}>
        <div className="border border-danger/40 bg-danger-soft px-5 py-6 text-danger">
          <p className="font-medium">this pull request page could not be loaded</p>
          <p className="mt-1">
            the request to village failed before the attachment could be read, so nothing is
            shown rather than a partial page. reload to try again.
          </p>
        </div>
      </div>
    );
  }

  if (attachment.isLoading || !data || !state) {
    return (
      <div className={shell}>
        <div className="h-4 w-40 animate-shimmer" />
        <div className="h-8 w-2/3 animate-shimmer" />
        <div className="h-64 w-full animate-shimmer" />
      </div>
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

  const subParts: string[] = [];
  if (data.attachment.head_ref) subParts.push(data.attachment.head_ref);
  if (digest) {
    const total = digest.header.commitsTotal;
    subParts.push(`${total} ${total === 1 ? "commit" : "commits"}`);
  }

  return (
    <div className={shell} data-testid="pull-request-page">
      <header className="flex flex-col gap-[var(--sp-2)] min-w-0">
        <p className="m-0 inline-flex items-center gap-[var(--sp-2)] font-mono text-[length:var(--fs-label)] text-ink-3">
          <GitPullRequest className="size-3.5 shrink-0" aria-hidden="true" />
          <span>{repoLabel}</span>
          <span className="tabular-nums">#{data.attachment.number}</span>
        </p>
        <div className="flex flex-wrap items-start justify-between gap-[var(--sp-3)]">
          <h1
            className="m-0 min-w-0 break-words normal-case font-[family-name:var(--font-display)] text-[length:var(--fs-xl)] font-bold leading-[1.25] text-[color:var(--ink-strong)]"
            data-testid="pull-request-title"
          >
            {pullTitle ?? `${repoLabel} #${data.attachment.number}`}
          </h1>
          <LinkButton
            as="a"
            href={githubHref}
            target="_blank"
            rel="noreferrer"
            variant="secondary"
            size="sm"
            iconRight={ExternalLink}
          >
            view on github
          </LinkButton>
        </div>
        {subParts.length > 0 && (
          <p
            className="m-0 font-mono text-[length:var(--fs-label)] text-ink-3 tabular-nums break-words"
            data-testid="pull-request-sub"
          >
            {subParts.join(" · ")}
          </p>
        )}
      </header>

      {viewerIsAuthor && (
        <AuthorPanel
          state={state}
          matching={digest ? digest.header.sessionCount : 0}
          audienceIds={audienceIds}
          audiences={audiences}
          pending={confirm.isPending || detach.isPending}
          error={confirm.error ?? detach.error}
          onAttach={() => {
            detach.reset();
            confirm.mutate();
          }}
          onDetach={() => {
            confirm.reset();
            detach.mutate();
          }}
        />
      )}

      {digest && (
        <p
          className="m-0 text-ink-2 leading-[var(--lh-body)]"
          data-testid="pull-request-coverage"
        >
          <span className="tabular-nums">{digest.header.commitsCovered}</span> of{" "}
          <span className="tabular-nums">{digest.header.commitsTotal}</span> commits traced
          {unlisted > 0 && (
            <>
              {" · "}
              <span className="tabular-nums">{unlisted}</span> attached{" "}
              {transcriptsNoun(unlisted)} {unlisted === 1 ? "is" : "are"} not listed here.
            </>
          )}
        </p>
      )}

      {titledDigest && titledDigest.items.length > 0 ? (
        <PromptDigest
          digest={titledDigest}
          layout="split"
          selected={selected}
          onSelect={(index) => setSelected(index)}
          itemHref={(item) => itemHref(item, data.attachment.owner, data.attachment.name)}
          className="border border-rule"
        />
      ) : digest ? (
        // The digest is present but has no rows: every bound prompt is gone. Say
        // so in words rather than rendering an empty shell where a digest was,
        // and say only that — which prompt left, and why, is its owner's.
        <section
          className="border border-rule bg-surface px-5 py-6 text-ink-2"
          data-testid="digest-empty"
        >
          <p className="m-0">no prompts are available for this pull request.</p>
        </section>
      ) : (
        <section
          className="border border-rule bg-surface px-5 py-6 text-ink-3"
          data-testid="digest-absent"
        >
          <p className="m-0">
            {state === "attached"
              ? "no prompts are shown for this pull request."
              : "the prompts appear here once the author attaches them."}
          </p>
        </section>
      )}
    </div>
  );
}

/**
 * The author's own panel: one plain line about the state the attachment is in,
 * the one or two actions it allows, and, while there is something to attach or
 * attached, who can read it.
 */
function AuthorPanel({
  state,
  matching,
  audienceIds,
  audiences,
  pending,
  error,
  onAttach,
  onDetach,
}: {
  state: VillagePullRequestAttachmentState;
  matching: number;
  audienceIds: string[];
  audiences: TranscriptAudience[] | null;
  pending: boolean;
  error: Error | null;
  onAttach: () => void;
  onDetach: () => void;
}) {
  let line: ReactNode;
  let actions: ReactNode = null;
  switch (state) {
    case "preview":
      line = (
        <>
          <strong className="font-bold text-[color:var(--ink-strong)]">
            <span className="tabular-nums">{matching}</span> of your transcripts{" "}
            {matching === 1 ? "matches" : "match"} this pull request.
          </strong>{" "}
          attach {matching === 1 ? "it so reviewers can read it" : "them so reviewers can read them"} next
          to the code.
        </>
      );
      actions = (
        <>
          <Button
            variant="primary"
            size="sm"
            onClick={onAttach}
            disabled={pending}
            data-testid="confirm-attachment"
          >
            attach <span className="tabular-nums">{matching}</span> {transcriptsNoun(matching)}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={onDetach}
            disabled={pending}
            data-testid="not-now-attachment"
          >
            not now
          </Button>
        </>
      );
      break;
    case "attached":
      line = (
        <strong className="font-bold text-[color:var(--ink-strong)]">
          <span className="tabular-nums">{matching}</span> of your transcripts{" "}
          {matching === 1 ? "is" : "are"} attached to this pull request.
        </strong>
      );
      actions = (
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
      break;
    case "waiting":
      line = (
        <>
          <strong className="font-bold text-[color:var(--ink-strong)]">
            none of your transcripts match this pull request yet.
          </strong>{" "}
          publish the session behind one of its commits and it is attached.
        </>
      );
      actions = (
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
      break;
    case "requested":
      line = (
        <>
          <strong className="font-bold text-[color:var(--ink-strong)]">
            you were asked to attach your transcripts.
          </strong>{" "}
          comment <code className="font-mono text-[length:var(--fs-sm)]">/peasant attach</code> on the
          pull request to review them here.
        </>
      );
      break;
    case "detached":
      line = (
        <>
          <strong className="font-bold text-[color:var(--ink-strong)]">
            you detached your transcripts from this pull request.
          </strong>{" "}
          comment <code className="font-mono text-[length:var(--fs-sm)]">/peasant attach</code> on it to
          attach them again.
        </>
      );
      break;
  }

  const showsAudience = audienceIds.length > 0;
  const sharedAudience =
    audiences && audiences.length > 0 && audiences.every((row) => row.audience === audiences[0].audience)
      ? audiences[0].audience
      : null;

  return (
    <section
      className="flex flex-col items-stretch gap-[var(--sp-2)] border border-rule-strong bg-[color:var(--surface-2)] p-[var(--sp-4)]"
      aria-label="your transcripts on this pull request"
      data-testid="pull-request-actions"
    >
      <div className="flex min-w-0 flex-col gap-[var(--sp-2)]">
        <p
          className="m-0 text-ink leading-[var(--lh-body)]"
          data-testid="attachment-state-line"
          data-state={state}
        >
          {line}
        </p>
        {showsAudience && (
          <div className="text-ink-2 leading-[var(--lh-body)]">
            {audiences === null ? (
              <p className="m-0" data-testid="audience-pending">
                attaching does not change who can read them.
              </p>
            ) : sharedAudience !== null ? (
              <p className="m-0">
                who can read {audiences.length === 1 ? "it" : "them"}:{" "}
                <span data-testid="transcript-audience">{sharedAudience}</span>. attaching does not
                change that.
              </p>
            ) : (
              <div className="flex min-w-0 flex-col gap-[var(--sp-1)]">
                <p className="m-0">who can read them (attaching does not change that):</p>
                <ul className="m-0 flex list-none flex-col gap-[var(--sp-1)] p-0">
                  {audiences.map((row, index) => (
                    <li key={row.transcriptId} className="min-w-0 break-words">
                      <span className="text-ink">{row.title?.trim() ? row.title : `session ${index + 1}`}</span>
                      {": "}
                      <span data-testid="transcript-audience">{row.audience}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
        {error && (
          <p data-testid="attachment-action-error" className="m-0 text-danger" role="alert">
            {error.message}
          </p>
        )}
      </div>
      {actions && <div className="flex flex-none flex-wrap items-center gap-[var(--sp-2)]">{actions}</div>}
    </section>
  );
}
