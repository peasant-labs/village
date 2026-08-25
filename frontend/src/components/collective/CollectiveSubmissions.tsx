"use client";

import { useState } from "react";
import Link from "next/link";
import { History } from "lucide-react";
import { DataState } from "@/lib/ft-ui";
import { useMyCollectiveSubmissions } from "@/lib/queries/collectives";
import { submissionPairChip } from "@/lib/shareEvents";
import { cn } from "@/lib/utils";
import ShareEventLog from "./ShareEventLog";

/**
 * The signed-in person's own submissions to ONE collective, each openable into
 * its full event history.
 *
 * The history request is per (transcript, collective) pair and is issued only
 * for the submission the reader opens, so reaching a profile never fans out
 * into a request per contribution.
 *
 * This reads the owner-only PAIRS endpoint
 * (`GET /users/me/collectives/{groupId}/submissions`), not the legacy
 * current-state list — every (transcript, collective) pair the owner has ever
 * had with this collective is a row, INCLUDING one whose every event ended in
 * a withdrawal and so has no current-state row left. That pair renders with
 * its history control intact and a chip reading "withdrawn"
 * ({@link submissionPairChip}). The server answers 404 — never a 200 with an
 * empty array — when the owner genuinely has none; {@link useMyCollectiveSubmissions}
 * normalizes that to an empty list, so the empty-state copy below means
 * exactly what it says regardless of which cause produced it.
 */
export default function CollectiveSubmissions({
  groupId,
  groupName,
}: {
  groupId: string;
  groupName: string;
}) {
  const { data, isLoading, isError, error } = useMyCollectiveSubmissions(groupId);
  const [openTranscriptId, setOpenTranscriptId] = useState<string | null>(null);
  const pairs = data ?? [];

  if (isError) {
    return (
      <p className="border border-danger/40 bg-danger-soft px-4 py-3 text-[13px] text-danger">
        your submissions to this collective could not be loaded:{" "}
        {String((error as Error)?.message ?? "the request failed")}. nothing was changed. retry, or
        reload this page.
      </p>
    );
  }

  return (
    <DataState
      loading={isLoading}
      empty={pairs.length === 0}
      emptyState={
        <p
          data-testid="collective-submissions-empty"
          className="px-1 py-2 font-mono text-xs text-ink-3"
        >
          no submissions of yours are on record in this collective
        </p>
      }
    >
      <ul data-testid="collective-submissions" className="flex flex-col gap-2">
        {pairs.map((pair) => {
          const open = openTranscriptId === pair.transcript_id;
          return (
            <li
              key={pair.transcript_id}
              data-testid="collective-submission"
              data-transcript-id={pair.transcript_id}
            >
              <div className="flex items-center gap-3 border border-rule bg-surface px-4 py-2.5">
                <Link
                  href={`/transcripts/${pair.transcript_id}`}
                  className="min-w-0 flex-1 truncate text-[13px] text-ink hover:text-ink-2 transition-colors focus-mono cursor-pointer"
                >
                  {pair.title ?? pair.transcript_id.slice(0, 8)}
                </Link>
                <span
                  data-testid="collective-submission-status"
                  data-status={pair.status}
                  className="font-mono text-xs text-ink-3 shrink-0"
                >
                  {submissionPairChip(pair.status)}
                </span>
                <button
                  type="button"
                  aria-expanded={open}
                  onClick={() =>
                    setOpenTranscriptId(open ? null : pair.transcript_id)
                  }
                  className={cn(
                    "inline-flex items-center gap-1.5 h-8 px-2.5 shrink-0",
                    "border border-rule bg-surface font-mono text-xs text-ink-3",
                    "hover:bg-surface-hover hover:text-ink transition-colors focus-mono cursor-pointer",
                  )}
                >
                  <History className="size-3.5" />
                  {open ? "hide history" : "history"}
                </button>
              </div>
              {open && (
                <div className="mt-2">
                  <p className="mb-2 font-mono text-xs text-ink-3">
                    every recorded event for this submission in {groupName}, oldest first
                  </p>
                  <ShareEventLog groupId={groupId} transcriptId={pair.transcript_id} />
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </DataState>
  );
}
