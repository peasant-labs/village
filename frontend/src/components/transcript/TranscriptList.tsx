"use client";

import { useState } from "react";
import Link from "next/link";
import { Pencil, Trash2 } from "lucide-react";
import { ProviderName, Tag, VisibilityEye } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import TranscriptEditDialog from "./TranscriptEditDialog";
import type { TranscriptListItem } from "@/lib/types";
import { resolveAttribution } from "@/lib/format";
import { isAgentSession } from "@/lib/sessionOrigin";
import { useDeleteTranscript } from "@/lib/queries/transcripts";
import { useAuth } from "@/providers/AuthProvider";
import { cn } from "@/lib/utils";

interface TranscriptListProps {
  items: TranscriptListItem[];
  /** When true, render Edit/Delete actions for rows the current viewer owns. */
  showOwnerActions?: boolean;
  /** Header text rendered above the list. Pass null/undefined to omit. */
  title?: string | null;
  /** Optional right-side counter / annotation in the header. */
  headerAside?: React.ReactNode;
  /** Empty-state body (rendered inside the panel when items is empty). */
  emptyState?: React.ReactNode;
  /** Drop the outer panel chrome — used when the list is embedded inside
   *  an existing bordered container (e.g. the user-profile library panel). */
  bare?: boolean;
  /** When true, hide the owner avatar + username pill (e.g. on /publish where
   *  everything is the viewer's own). The visibility eye still renders. */
  hideOwner?: boolean;
}

export default function TranscriptList({
  items,
  showOwnerActions = false,
  title,
  headerAside,
  emptyState,
  bare = false,
  hideOwner = false,
}: TranscriptListProps) {
  const { user } = useAuth();
  const viewerId = user?.id;

  const rows =
    items.length > 0 ? (
      <div className="divide-y divide-rule">
        {items.map((item) => (
          <Row
            key={item.transcript.id}
            item={item}
            canManage={showOwnerActions && viewerId === item.owner.id}
            hideOwner={hideOwner}
          />
        ))}
      </div>
    ) : (
      emptyState ?? null
    );

  if (bare) return <>{rows}</>;

  return (
    <section className="border border-rule bg-surface">
      {title != null && (
        <div className="flex items-center justify-between border-b border-rule px-5 py-3">
          <span className="text-sm font-medium text-ink">{title}</span>
          {headerAside}
        </div>
      )}
      {rows}
    </section>
  );
}

function Row({
  item,
  canManage,
  hideOwner,
}: {
  item: TranscriptListItem;
  canManage: boolean;
  hideOwner: boolean;
}) {
  const { transcript: t, owner, shares } = item;
  const { user: viewer } = useAuth();
  const attribution = resolveAttribution(owner, viewer?.id);
  const [editOpen, setEditOpen] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const del = useDeleteTranscript();

  const displayTitle = t.title || "Untitled";

  function handleDelete(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    del.mutate(t.id);
  }

  return (
    <div className="group relative flex items-center gap-3 px-5 py-3 transition-colors hover:bg-surface-hover">
      {/* The whole row is a link to the detail page, except for the action
          area on the right. The link is absolutely positioned over the row
          and the action cluster sits in front with z-index. */}
      <Link
        href={`/transcripts/${t.id}`}
        className="absolute inset-0 focus-mono cursor-pointer"
        aria-label={`Open transcript ${displayTitle}`}
      />

      <div className="min-w-0 flex-1 flex flex-col gap-0.5">
        <span className="text-sm text-ink font-medium truncate">
          {displayTitle}
        </span>
        <span className="text-[12px] text-ink-3 flex items-center gap-1.5 truncate">
          {isHarness(t.model_provider) ? (
            <ProviderName harness={t.model_provider} />
          ) : (
            <Tag>{t.model_provider}</Tag>
          )}
          <span className="text-rule">&middot;</span>
          <span className="font-mono tabular-nums">
            {new Date(t.published_at).toLocaleDateString("en-US", {
              month: "short",
              day: "numeric",
              year: "numeric",
            })}
          </span>
          {t.turn_count != null && (
            <>
              <span className="text-rule">&middot;</span>
              <span className="font-mono tabular-nums">
                {t.turn_count} turns
              </span>
            </>
          )}
          {/* The row says what it is wherever it appears, so a session that
              reached the page through a direct link or a collective list is
              labelled the same way it is inside the collapsed group. */}
          {isAgentSession(t.session_origin) && (
            <>
              <span className="text-rule">&middot;</span>
              <span
                data-testid="agent-session-badge"
                className="font-mono text-[11px] text-ink-3 border border-rule px-1"
              >
                agent session
              </span>
            </>
          )}
        </span>
      </div>

      <div className="relative z-10 flex items-center gap-2 shrink-0">
        {!hideOwner && (
          <>
            {attribution.anonymous ? (
              <div className="w-4 h-4 bg-surface-hover border border-rule flex items-center justify-center text-[8px] font-bold text-ink-3">
                ?
              </div>
            ) : owner.avatar_url ? (
              <img
                src={owner.avatar_url}
                alt=""
                className="w-4 h-4 border border-rule"
              />
            ) : (
              <div className="w-4 h-4 bg-surface-hover border border-rule flex items-center justify-center text-[8px] font-bold text-ink-2">
                {owner.github_username[0].toUpperCase()}
              </div>
            )}
            <span className="text-xs text-ink-3">{attribution.label}</span>
          </>
        )}
        <VisibilityEye
          visibility={t.visibility}
          sharedWith={
            shares && shares.length > 0
              ? shares.map((s) => s.group_name).join(", ")
              : undefined
          }
        />
      </div>

      {canManage && (
        <div
          className="relative z-10 flex items-center gap-1"
          onClick={(e) => e.stopPropagation()}
        >
          {confirmingDelete ? (
            <>
              <span className="font-mono text-[11px] text-ink-3">Delete?</span>
              <button
                type="button"
                disabled={del.isPending}
                onClick={handleDelete}
                className={cn(
                  "inline-flex items-center gap-1 h-7 px-2 text-[11.5px] font-medium",
                  "border border-danger/40 bg-danger-soft text-danger",
                  "hover:bg-danger hover:text-danger-fg focus-mono transition-colors cursor-pointer",
                  "disabled:opacity-50 disabled:cursor-not-allowed",
                )}
              >
                {del.isPending ? "Removing…" : "Yes"}
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.preventDefault();
                  setConfirmingDelete(false);
                }}
                className={cn(
                  "inline-flex items-center gap-1 h-7 px-2 text-[11.5px] font-medium",
                  "border border-rule bg-surface text-ink-2",
                  "hover:bg-surface-hover focus-mono transition-colors cursor-pointer",
                )}
              >
                Cancel
              </button>
            </>
          ) : (
            <>
              <button
                type="button"
                title="Edit"
                aria-label="Edit transcript"
                onClick={(e) => {
                  e.preventDefault();
                  setEditOpen(true);
                }}
                className={cn(
                  "inline-flex items-center justify-center w-7 h-7",
                  "border border-rule bg-surface text-ink-3",
                  "hover:bg-surface-hover hover:text-ink focus-mono transition-colors cursor-pointer",
                )}
              >
                <Pencil size={12} strokeWidth={1.75} />
              </button>
              <button
                type="button"
                title="Delete"
                aria-label="Delete transcript"
                onClick={(e) => {
                  e.preventDefault();
                  setConfirmingDelete(true);
                }}
                className={cn(
                  "inline-flex items-center justify-center w-7 h-7",
                  "border border-rule bg-surface text-ink-3",
                  "hover:bg-danger-soft hover:text-danger focus-mono transition-colors cursor-pointer",
                )}
              >
                <Trash2 size={12} strokeWidth={1.75} />
              </button>
            </>
          )}
          <TranscriptEditDialog
            open={editOpen}
            onClose={() => setEditOpen(false)}
            transcriptId={t.id}
            initialTitle={t.title}
            initialVisibility={t.visibility}
          />
        </div>
      )}
    </div>
  );
}
