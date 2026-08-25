"use client";

import { useState } from "react";
import Link from "next/link";
import { History } from "lucide-react";
import { DataState } from "@/lib/ft-ui";
import { useMyGroupShares } from "@/lib/queries/groups";
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
 * KNOWN LIMIT OF THE AVAILABLE WIRE, stated because it is visible to the
 * reader: this list comes from `GET /groups/{id}/my-shares`, which serves the
 * current state of each submission. A contribution whose every event ended in
 * a withdrawal has no current state left, so it has no row here and its
 * history has no entry point on this surface. The history ITSELF is complete
 * whenever it is reachable: withdrawals appear in it as their own events.
 */
export default function CollectiveSubmissions({
  groupId,
  groupName,
}: {
  groupId: string;
  groupName: string;
}) {
  const { data, isLoading, isError, error } = useMyGroupShares(groupId);
  const [openTranscriptId, setOpenTranscriptId] = useState<string | null>(null);
  const shares = data ?? [];

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
      empty={shares.length === 0}
      emptyState={
        <p className="px-1 py-2 font-mono text-xs text-ink-3">
          no submissions of yours are on record in this collective
        </p>
      }
    >
      <ul data-testid="collective-submissions" className="flex flex-col gap-2">
        {shares.map((share) => {
          const open = openTranscriptId === share.id;
          return (
            <li key={share.id} data-testid="collective-submission" data-transcript-id={share.id}>
              <div className="flex items-center gap-3 border border-rule bg-surface px-4 py-2.5">
                <Link
                  href={`/transcripts/${share.id}`}
                  className="min-w-0 flex-1 truncate text-[13px] text-ink hover:text-ink-2 transition-colors focus-mono cursor-pointer"
                >
                  {share.title ?? share.id.slice(0, 8)}
                </Link>
                <span className="font-mono text-xs text-ink-3 shrink-0">{share.status}</span>
                <button
                  type="button"
                  aria-expanded={open}
                  onClick={() => setOpenTranscriptId(open ? null : share.id)}
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
                  <ShareEventLog groupId={groupId} transcriptId={share.id} />
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </DataState>
  );
}
