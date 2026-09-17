"use client";

import { use } from "react";
import Link from "next/link";
import { Check, Info, Layers, Unlink } from "lucide-react";
import { Button, EmptyState, PromptDigest, Tag, Tooltip } from "@/lib/ft-ui";
import { isApiErrorStatus } from "@/lib/api";
import {
  useConfirmPullRequestAttachment,
  useDetachPullRequestAttachment,
  usePullRequestAttachment,
} from "@/lib/queries/pulls";
import type {
  PromptDigestItem,
  VillagePullRequestAttachedTranscript,
} from "@peasant-labs/schema";

/**
 * The pull request page: `/pulls/{owner}/{name}/{number}`.
 *
 * It shows the digest fairtrade renders from the schema type — the design system
 * owns how a digest reads, this page owns only where each item links and the
 * author's confirm/detach — plus the attachment's own state and the transcripts
 * it holds.
 *
 * The wire carries the repository owner and name but not its host, and GitHub is
 * the only provider Village connects today, so a commit anchor links to GitHub.
 * If another host is ever supported, that becomes a contract question rather
 * than a second string built here.
 */
const COMMIT_HOST = "https://github.com";

/** One chain item's destination: the turn, the transcript, or the commit. */
function itemHref(item: PromptDigestItem, owner: string, name: string): string {
  if (item.kind === "commit") {
    return `${COMMIT_HOST}/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/commit/${encodeURIComponent(item.commitSha ?? item.text)}`;
  }
  if ((item.kind === "prompt" || item.kind === "skill") && item.turnIndex != null) {
    return `/transcripts/${item.transcriptId}?turn=${item.turnIndex}`;
  }
  return `/transcripts/${item.transcriptId}`;
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

  const shell = "max-w-[1000px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up";

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
            message="Village has no prompt attachment for this pull request that you can open."
            action={
              <Link
                href="/collectives"
                className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
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
        <div className="border border-danger/40 bg-danger-soft px-5 py-6 text-sm text-danger">
          <p className="font-medium">This pull request page could not be loaded</p>
          <p className="mt-1 text-[13px]">
            The request to Village failed before the attachment could be read, so nothing is
            shown rather than a partial page. Reload to try again.
          </p>
        </div>
      </div>
    );
  }

  if (attachment.isLoading || !attachment.data) {
    return (
      <div className={shell}>
        <div className="h-4 w-40 animate-shimmer" />
        <div className="h-8 w-2/3 animate-shimmer" />
        <div className="h-64 w-full animate-shimmer" />
      </div>
    );
  }

  const data = attachment.data;
  const state = data.attachment.state;
  // A transcript is "advertised" when the digest still carries an item for it.
  // The digest is built from every bound transcript and gives each one a session
  // item, so a row the digest no longer lists is a row it dropped. A missing
  // digest means there is nothing to compare against, so nothing is marked:
  // silence is the only safe direction when the evidence is absent.
  const advertisedTranscriptIds = data.digest
    ? new Set(data.digest.items.map((item) => item.transcriptId))
    : null;
  const viewerIsAuthor = data.viewer_is_author;
  // The last action to fail is what the reader needs to see; a success clears
  // the other mutation's error so a stale message cannot outlive the attempt it
  // belonged to (both are reset before the next action runs).
  const actionError = confirm.error ?? detach.error;
  const actionPending = confirm.isPending || detach.isPending;
  // The backend permits detaching a waiting request too, so the author can
  // withdraw before anything is matched rather than waiting on a 409.
  const canDetach = state === "preview" || state === "attached" || state === "waiting";

  return (
    <div className={shell} data-testid="pull-request-page">
      <header className="flex flex-col gap-1">
        <div className="flex items-center gap-2 text-ink-3">
          <Layers className="size-3.5" />
          <span className="text-xs font-mono lowercase">pull request prompts</span>
        </div>
        <h1 className="text-lg font-medium text-ink break-words" data-testid="pull-request-title">
          {data.attachment.owner}/{data.attachment.name} #{data.attachment.number}
        </h1>
        <div className="flex items-center gap-2 text-[13px] text-ink-3">
          <Tag>{state}</Tag>
          {data.attachment.is_private_repository && (
            <span className="text-xs lowercase">private repository</span>
          )}
          <span className="font-mono text-xs tabular-nums">
            {data.attachment.head_sha.slice(0, 7)}
          </span>
        </div>
      </header>

      {viewerIsAuthor && (state === "preview" || canDetach) && (
        <section
          className="border border-rule bg-surface px-5 py-4 flex flex-col gap-3"
          aria-label="attachment actions"
          data-testid="pull-request-actions"
        >
          <div className="flex items-center gap-2">
            {state === "preview" && (
              <Button
                onClick={() => {
                  detach.reset();
                  confirm.mutate();
                }}
                disabled={actionPending}
                data-testid="confirm-attachment"
              >
                <Check className="size-3.5" />
                attach these prompts
              </Button>
            )}
            {canDetach && (
              <Button
                variant="ghost"
                onClick={() => {
                  confirm.reset();
                  detach.mutate();
                }}
                disabled={actionPending}
                data-testid="detach-attachment"
              >
                <Unlink className="size-3.5" />
                detach
              </Button>
            )}
          </div>
          {actionError && (
            <p
              data-testid="attachment-action-error"
              className="text-[13px] text-danger"
              role="alert"
            >
              {actionError.message}
            </p>
          )}
        </section>
      )}

      {data.digest ? (
        <PromptDigest
          digest={data.digest}
          itemHref={(item) => itemHref(item, data.attachment.owner, data.attachment.name)}
          className="border border-rule"
        />
      ) : (
        <section className="border border-rule bg-surface px-5 py-6 text-sm text-ink-3">
          <p>
            The digest is shown to the pull request&apos;s author, or to anyone once it is
            attached.
          </p>
        </section>
      )}

      {data.transcripts.length > 0 && (
        <AttachedTranscripts
          transcripts={data.transcripts}
          advertisedTranscriptIds={advertisedTranscriptIds}
        />
      )}
    </div>
  );
}

function AttachedTranscripts({
  transcripts,
  advertisedTranscriptIds,
}: {
  transcripts: VillagePullRequestAttachedTranscript[];
  advertisedTranscriptIds: Set<string> | null;
}) {
  return (
    <section className="border border-rule bg-surface" aria-label="attached transcripts">
      <div className="px-5 py-3 border-b border-rule flex items-center justify-between">
        <span className="text-xs font-mono lowercase text-ink-3">attached transcripts</span>
        <span className="text-xs font-mono text-ink-3 tabular-nums">
          {transcripts.length}
        </span>
      </div>
      <ul className="flex flex-col">
        {transcripts.map((transcript) => (
          <li
            key={transcript.transcript_id}
            className="px-5 py-3 border-b border-rule last:border-b-0 flex items-center justify-between gap-3"
          >
            <Link
              href={`/transcripts/${transcript.transcript_id}`}
              className="text-sm text-ink min-w-0 break-words hover:text-ink-2 transition-colors focus-mono cursor-pointer"
            >
              {transcript.title ?? transcript.transcript_id}
            </Link>
            <span className="flex shrink-0 items-center gap-3">
              {advertisedTranscriptIds !== null &&
                !advertisedTranscriptIds.has(transcript.transcript_id) && (
                  <span className="flex items-center gap-1 text-xs font-mono lowercase text-ink-3">
                    not available
                    <Tooltip
                      content="this transcript is attached, but the digest no longer lists it. the prompts behind it may have been withdrawn, or the digest may predate them."
                      id={`transcript-not-available-${transcript.transcript_id}`}
                    >
                      <button
                        type="button"
                        aria-label="why this transcript is not listed in the digest"
                        className="cursor-help text-ink-3 hover:text-ink focus-mono"
                      >
                        <Info className="size-3.5" aria-hidden="true" />
                      </button>
                    </Tooltip>
                  </span>
                )}
              <span className="text-xs font-mono text-ink-3 tabular-nums">
                visibility before attach: {transcript.previous_visibility}
              </span>
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
