"use client";

import { Ban, Check, CornerUpLeft, Send, X } from "lucide-react";
import type { ComponentType } from "react";
import { DataState } from "@/lib/ft-ui";
import { useShareEventHistory } from "@/lib/queries/collectives";
import { shareEventLabel, shareEventTime } from "@/lib/shareEvents";
import {
  assertShareEventStatusExhaustive,
  type ShareEventStatus,
} from "@/lib/types";

/**
 * The glyph for one event outcome. Every outcome gets its OWN glyph: meaning is
 * never carried by position or colour alone, and a withdrawal must not borrow
 * a refusal's mark, because a person scanning this log is trying to tell those
 * two apart.
 */
function eventIcon(status: ShareEventStatus): ComponentType<{ className?: string; size?: number }> {
  switch (status) {
    case "pending":
      return Send;
    case "approved":
      return Check;
    case "rejected":
      return X;
    case "retracted":
      return CornerUpLeft;
    case "revoked":
      return Ban;
    default:
      return assertShareEventStatusExhaustive(status);
  }
}

/**
 * The owner-only audit log of one transcript's life inside one collective.
 *
 * EVERY state change is an event, numbered in the order it happened and read
 * top to bottom: submissions, decisions, and withdrawals alike. The ordinal is
 * labelled "event" and never "attempt", because the sequence contains events
 * nobody submitted — a collective removing a contribution is an event in this
 * log, and calling it an attempt would be a lie about who acted.
 *
 * Each row names its actor class ("retracted by owner", "revoked by
 * collective"). The wire carries no user id and no name, so nothing here is
 * looked up and nothing can be missing; an event that has not been decided
 * simply has no actor clause.
 */
export default function ShareEventLog({
  groupId,
  transcriptId,
}: {
  groupId: string;
  transcriptId: string;
}) {
  const { data, isLoading, isError, error } = useShareEventHistory(groupId, transcriptId, true);
  const events = data ?? [];

  // The failure is reported here rather than through DataState's own
  // disconnected panel: that panel's copy names a local program on the
  // reader's own machine, which is not what failed when a village request
  // does. Loading and empty still go through DataState.
  if (isError) {
    return (
      <p className="border border-danger/40 bg-danger-soft px-4 py-3 text-[length:var(--fs-body)] text-danger">
        the event history for this submission could not be loaded:{" "}
        {String((error as Error)?.message ?? "the request failed")}. nothing was changed. retry, or
        reload this page.
      </p>
    );
  }

  return (
    <DataState
      loading={isLoading}
      empty={events.length === 0}
      emptyState={
        <p className="px-4 py-3 font-mono text-[length:var(--fs-label)] text-ink-3">
          no events recorded for this submission
        </p>
      }
    >
      <ol
        data-testid="share-event-log"
        className="on-history-events"
      >
        {events.map((event) => {
          const Icon = eventIcon(event.status);
          const time = shareEventTime(event);
          return (
            <li
              key={event.event_num}
              data-testid="share-event"
              data-event-num={event.event_num}
              data-event-status={event.status}
              className="on-history-event"
            >
              <span className="font-mono text-[length:var(--fs-label)] text-ink-4 tabular-nums shrink-0">
                event {event.event_num}
              </span>
              <Icon className="size-3.5 text-ink-3 shrink-0" />
              <span className="font-mono text-[length:var(--fs-label)] text-ink whitespace-nowrap">{shareEventLabel(event)}</span>
              {/* The time this row may honestly show: the decision time when
                  the ledger recorded one, the submission time while the
                  attempt is still open, and an explicit unknown label for a
                  terminal attempt whose decision time was never captured. The
                  submission time is never presented as a decision time. */}
              <span
                data-testid="share-event-time"
                data-time-known={time.known ? "true" : "false"}
                className="ml-auto font-mono text-[length:var(--fs-label)] text-ink-3 tabular-nums shrink-0"
              >
                {time.text}
              </span>
            </li>
          );
        })}
      </ol>
    </DataState>
  );
}
