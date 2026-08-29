"use client";

import { useMemo, useState } from "react";
import TranscriptList from "./TranscriptList";
import SessionGroupDisclosure from "./SessionGroupDisclosure";
import { useTranscripts } from "@/lib/queries/transcripts";
import { AGENT_ORIGIN, agentSessionGroupLabel } from "@/lib/sessionOrigin";
import { TRANSCRIPT_PAGE_SIZE } from "@/lib/transcriptPageRequest";

interface AgentSessionGroupProps {
  /** How many agent-driven sessions the surrounding list's filters match. */
  agentTotal: number;
  /**
   * The surrounding list's server-affecting filters (owner, search, provider,
   * tags, sort). The group re-asks the same question narrowed to agent
   * sessions, so its rows always belong to the list they sit under. `page` and
   * `limit` are ignored: the group pages independently of the list above it.
   */
  baseParams?: Record<string, string>;
  /** Render Edit/Delete actions for rows the current viewer owns. */
  showOwnerActions?: boolean;
  /** Drop the outer panel border when the group sits inside a bordered panel. */
  bare?: boolean;
}

/** The id the group's control names and its rows element carries. */
const AGENT_SESSION_GROUP_ROWS_ID = "agent-session-group-rows";

/**
 * The collapsed group of agent-driven sessions that sits at the end of a
 * transcript list.
 *
 * Sessions with no human prompt in the transcript would otherwise fill a
 * publisher's root-level list, so the server leaves them out and reports how
 * many it left out. This renders that count as one row the viewer can expand;
 * expanding asks the same endpoint for exactly those rows. Nothing here hides
 * anything: every row it lists links to its transcript page as usual.
 *
 * Collapse state is per-page UI state and deliberately not persisted: the
 * group is an aside, and a viewer who expanded it once has not asked for it to
 * be open on every future visit.
 */
export default function AgentSessionGroup({
  agentTotal,
  baseParams,
  showOwnerActions = false,
  bare = false,
}: AgentSessionGroupProps) {
  const [expanded, setExpanded] = useState(false);

  if (agentTotal <= 0) return null;

  const label = agentSessionGroupLabel(agentTotal);

  return (
    <SessionGroupDisclosure
      label={label}
      // The agent group keeps the `+` it has always announced itself with.
      collapsedLabel={`+ ${label}`}
      expanded={expanded}
      onToggle={() => setExpanded((open) => !open)}
      rowsID={AGENT_SESSION_GROUP_ROWS_ID}
      testID="agent-session-group"
      bare={bare}
    >
      <AgentSessionRows
        agentTotal={agentTotal}
        baseParams={baseParams}
        label={label}
        showOwnerActions={showOwnerActions}
      />
    </SessionGroupDisclosure>
  );
}

/**
 * The expanded rows. This lives in its own component so the request for agent
 * sessions is made ONLY while the group is open: a collapsed group must not
 * add a second list request to every page load, and a hook cannot be skipped
 * inside a component that is already mounted.
 */
function AgentSessionRows({
  agentTotal,
  baseParams,
  label,
  showOwnerActions,
}: {
  agentTotal: number;
  baseParams?: Record<string, string>;
  label: string;
  showOwnerActions: boolean;
}) {
  const [page, setPage] = useState(1);

  const params = useMemo(() => {
    const next: Record<string, string> = { ...(baseParams ?? {}) };
    delete next.page;
    delete next.limit;
    next.origin = AGENT_ORIGIN;
    next.page = String(page);
    next.limit = String(TRANSCRIPT_PAGE_SIZE);
    return next;
  }, [baseParams, page]);

  const { data, isLoading, isError, error, refetch } = useTranscripts(params);
  const items = data?.transcripts ?? [];
  const totalPages = Math.max(1, Math.ceil(agentTotal / TRANSCRIPT_PAGE_SIZE));

  return (
    <div id={AGENT_SESSION_GROUP_ROWS_ID} data-testid="agent-session-group-rows">
      {isError ? (
        <div
          role="alert"
          className="border-t border-rule px-5 py-4 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3"
        >
          <p className="text-[13px] text-ink-3">
            {`Failed to load the ${label} for this list. `}
            {error instanceof Error ? error.message : "Unknown error."}
            {" The sessions are still reachable from their own links. Retry to load the group."}
          </p>
          <button
            type="button"
            className="btn btn-secondary btn-sm shrink-0"
            onClick={() => refetch()}
          >
            retry
          </button>
        </div>
      ) : isLoading ? (
        <div className="border-t border-rule px-5 py-4">
          <div className="h-9 animate-shimmer" />
        </div>
      ) : (
        <div className="border-t border-rule">
          <TranscriptList items={items} showOwnerActions={showOwnerActions} bare />
          {totalPages > 1 && (
            <div className="flex items-center gap-3 px-5 py-3 border-t border-rule">
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={page <= 1}
                onClick={() => setPage((current) => Math.max(1, current - 1))}
              >
                previous
              </button>
              <span className="font-mono text-xs text-ink-3 tabular-nums">
                page {page} of {totalPages}
              </span>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={page >= totalPages}
                onClick={() => setPage((current) => Math.min(totalPages, current + 1))}
              >
                next
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
