"use client";

import { use } from "react";
import Link from "next/link";
import { Check, GitCommitHorizontal, Layers, Unlink } from "lucide-react";
import { Button, EmptyState, Tag } from "@/lib/ft-ui";
import { isApiErrorStatus } from "@/lib/api";
import {
  useConfirmPullRequestAttachment,
  useDetachPullRequestAttachment,
  usePullRequestAttachment,
} from "@/lib/queries/pulls";
import type {
  PromptDigest,
  PromptDigestItem,
  VillagePullRequestAttachedTranscript,
} from "@peasant-labs/schema";

/**
 * The pull request page: `/pulls/{owner}/{name}/{number}`.
 *
 * It renders the digest the server already computed, plainly: the header line,
 * the skills list, and the chain of sessions, prompts, skills and commit
 * anchors. It deliberately does not depend on a fairtrade digest component
 * (none is published yet), so adopting one later does not change this route's
 * shape.
 *
 * The wire carries the repository owner and name but not its host, and GitHub is
 * the only provider Village connects today, so a commit anchor links to GitHub.
 * If another host is ever supported, that becomes a contract question rather
 * than a second string built here.
 */
const COMMIT_HOST = "https://github.com";

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

  // ONE refusal for every case where no attachment is readable here: a missing
  // pull request, a private repository's attachment the caller may not see, and
  // an unlinked repository are deliberately indistinguishable.
  if (isApiErrorStatus(attachment.error, 404)) {
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
  const viewerIsAuthor = data.viewer_is_author;
  const actionError = confirm.error ?? detach.error;
  const actionPending = confirm.isPending || detach.isPending;

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

      {viewerIsAuthor && (state === "preview" || state === "attached") && (
        <section
          className="border border-rule bg-surface px-5 py-4 flex flex-col gap-3"
          aria-label="attachment actions"
          data-testid="pull-request-actions"
        >
          <div className="flex items-center gap-2">
            {state === "preview" && (
              <Button
                onClick={() => confirm.mutate()}
                disabled={actionPending}
                data-testid="confirm-attachment"
              >
                <Check className="size-3.5" />
                attach these prompts
              </Button>
            )}
            <Button
              variant="ghost"
              onClick={() => detach.mutate()}
              disabled={actionPending}
              data-testid="detach-attachment"
            >
              <Unlink className="size-3.5" />
              detach
            </Button>
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
        <DigestView digest={data.digest} owner={owner} name={name} />
      ) : (
        <section className="border border-rule bg-surface px-5 py-6 text-sm text-ink-3">
          <p>
            The digest is shown to the pull request&apos;s author, or to anyone once it is
            attached.
          </p>
        </section>
      )}

      {data.transcripts.length > 0 && (
        <AttachedTranscripts transcripts={data.transcripts} />
      )}
    </div>
  );
}

function DigestView({
  digest,
  owner,
  name,
}: {
  digest: PromptDigest;
  owner: string;
  name: string;
}) {
  return (
    <section className="border border-rule bg-surface" aria-label="prompt digest">
      <div className="px-5 py-3 border-b border-rule flex items-baseline justify-between gap-3">
        <span className="text-xs font-mono lowercase text-ink-3">digest</span>
        <span className="text-xs font-mono text-ink-3 tabular-nums">
          {digest.header.sessionCount} sessions · {digest.header.promptCount} prompts ·{" "}
          {digest.header.commitsCovered}/{digest.header.commitsTotal} commits
        </span>
      </div>

      {digest.skills.length > 0 && (
        <div className="px-5 py-3 border-b border-rule flex flex-wrap items-center gap-2">
          <span className="text-xs font-mono lowercase text-ink-3">skills</span>
          {digest.skills.map((skill) => (
            <span
              key={skill.name}
              className="text-[13px] text-ink-2 font-mono"
              title={`${skill.invocationCount} invocations`}
            >
              /{skill.name}
              <span className="text-ink-3 tabular-nums"> ×{skill.invocationCount}</span>
            </span>
          ))}
        </div>
      )}

      <ol className="flex flex-col">
        {digest.items.map((item, index) => (
          <DigestRow
            key={`${item.kind}-${item.transcriptId}-${index}`}
            item={item}
            owner={owner}
            name={name}
          />
        ))}
      </ol>
    </section>
  );
}

/**
 * One chain entry. A prompt or a skill links to the exact turn it came from; a
 * session boundary links to its transcript; a commit anchor links to the commit
 * itself, which is the only thing a reader can verify outside Village.
 */
function DigestRow({
  item,
  owner,
  name,
}: {
  item: PromptDigestItem;
  owner: string;
  name: string;
}) {
  const transcriptHref =
    item.turnIndex != null
      ? `/transcripts/${item.transcriptId}?turn=${item.turnIndex}`
      : `/transcripts/${item.transcriptId}`;
  const linkClass = "hover:text-ink transition-colors focus-mono cursor-pointer";

  if (item.kind === "session") {
    return (
      <li className="px-5 py-3 border-b border-rule last:border-b-0 flex items-center justify-between gap-3 bg-surface-elev">
        <Link
          href={transcriptHref}
          className={`text-sm text-ink-2 lowercase min-w-0 break-words ${linkClass}`}
        >
          {item.text}
        </Link>
        <span className="text-xs font-mono text-ink-3 tabular-nums">
          {item.promptCount ?? 0} prompts · {item.commitCount ?? 0} commits
        </span>
      </li>
    );
  }

  if (item.kind === "prompt") {
    return (
      <li className="px-5 py-3 border-b border-rule last:border-b-0 flex gap-3">
        <span className="text-xs font-mono text-ink-3 tabular-nums w-8 shrink-0">
          {item.ordinal}
        </span>
        {/* A digest carries the COMPLETE chain, and a prompt's text is the whole
            turn content, so this row is a preview rather than the full text: it
            is clamped to a few lines and the link opens the exact turn. Without
            the clamp a session with hundreds of long prompts renders an
            unreadable wall, and without break-words a single unbroken token (a
            URL, minified code) overflows the panel. */}
        <Link
          href={transcriptHref}
          title={item.text}
          data-testid="digest-prompt"
          className={`text-sm text-ink min-w-0 line-clamp-6 whitespace-pre-wrap break-words ${linkClass}`}
        >
          {item.text}
        </Link>
      </li>
    );
  }

  if (item.kind === "skill") {
    return (
      <li className="px-5 py-3 border-b border-rule last:border-b-0 flex gap-3">
        <span className="text-xs font-mono text-ink-3 w-8 shrink-0">skill</span>
        <Link
          href={transcriptHref}
          className={`text-sm text-ink-2 font-mono min-w-0 break-words ${linkClass}`}
        >
          {item.text}
          {item.args ? <span className="text-ink-3"> {item.args}</span> : null}
        </Link>
      </li>
    );
  }

  const repoHref = `${COMMIT_HOST}/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/commit/${item.commitSha ?? item.text}`;
  return (
    <li className="px-5 py-3 border-b border-rule last:border-b-0 flex items-center justify-between gap-3">
      <a
        href={repoHref}
        target="_blank"
        rel="noreferrer noopener"
        className={`text-[13px] text-ink-2 font-mono flex items-center gap-2 ${linkClass}`}
      >
        <GitCommitHorizontal className="size-3.5 text-ink-3" />
        {(item.commitSha ?? item.text).slice(0, 7)}
      </a>
      {item.additions != null && (
        <span className="text-xs font-mono text-ink-3 tabular-nums">
          +{item.additions} −{item.deletions ?? 0} · {item.filesChanged ?? 0} files
        </span>
      )}
    </li>
  );
}

function AttachedTranscripts({
  transcripts,
}: {
  transcripts: VillagePullRequestAttachedTranscript[];
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
            <span className="text-xs font-mono text-ink-3 tabular-nums">
              {transcript.previous_visibility} before
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
