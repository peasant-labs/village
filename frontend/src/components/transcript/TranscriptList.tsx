"use client";

import { Fragment, useState } from "react";
import Link from "next/link";
import { Pencil, Trash2 } from "lucide-react";
import { ProviderName, Tag, VisibilityEye } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import TranscriptEditDialog from "./TranscriptEditDialog";
import ChildSessionDisclosure from "./ChildSessionDisclosure";
import type { Transcript, TranscriptRow } from "@/lib/types";
import {
  formatCompact,
  formatModelName,
  resolveAttribution,
  transcriptTokens,
} from "@/lib/format";
import { isAgentSession } from "@/lib/sessionOrigin";
import { useDeleteTranscript } from "@/lib/queries/transcripts";
import { useAuth } from "@/providers/AuthProvider";
import { cn } from "@/lib/utils";

/**
 * One fact a row can state on the line under its title.
 *
 * A closed set rather than free-form nodes, so every list in this app states a
 * fact the SAME way: a collective's browse table and a repository's rows carry
 * different columns, and before this they each spelled a token count and a date
 * in their own words. A surface names the facts it wants; it does not describe
 * how one is drawn.
 *
 * A new member is added HERE, once, and is then available to every list.
 */
export type TranscriptRowFact =
  | "provider"
  | "model"
  | "date"
  | "turns"
  | "tokens"
  | "branch";

/**
 * What a row states when its list says nothing: what every transcript list
 * showed before a list could choose, so an existing caller is unchanged.
 */
export const DEFAULT_TRANSCRIPT_ROW_FACTS: readonly TranscriptRowFact[] = [
  "provider",
  "date",
  "turns",
];

/**
 * A list where the viewer picks rows out to act on together.
 *
 * The selected set and the action over it belong to the surface, not to the
 * list: a collective owner selects contributions in order to remove them from
 * the collective, which is that page's decision to make. The list only draws
 * the box and reports the click.
 *
 * A folded child row carries a box of its own, so a session started by another
 * session can be picked out exactly like any other row.
 */
export interface TranscriptRowSelection {
  /** Transcript ids currently picked out. */
  selectedIDs: ReadonlySet<string>;
  /** The viewer clicked one row's box. */
  onToggle: (transcriptID: string) => void;
}

interface TranscriptListProps {
  items: TranscriptRow[];
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
  /** The sessions each row started, keyed by that row's `transcript.id`.
   *
   *  A row named here renders an expandable chip beneath it holding those
   *  sessions. Omitting this prop is what a list that has no answer to "whose
   *  children are these" passes: discovery folds started sessions away without
   *  offering them anywhere, so it passes nothing and no chip is drawn.
   *
   *  The caller owns the grouping because the caller owns the rows: a list that
   *  shows only its five most recent parents still hangs every child off them,
   *  which it can only do by grouping before it slices. */
  childSessions?: Map<string, TranscriptRow[]>;
  /** The facts each row states under its title, in the order given. Defaults to
   *  {@link DEFAULT_TRANSCRIPT_ROW_FACTS}. */
  facts?: readonly TranscriptRowFact[];
  /** Draw a selection box on every row and report clicks. Omit on a list the
   *  viewer does not select from. */
  selection?: TranscriptRowSelection;
  /** The viewer may read the handle of an author who opted out of being
   *  discoverable. True only where the surface itself grants it -- a
   *  collective's owner browsing their own collective's contributions -- never
   *  as a default. */
  viewerIsPrivileged?: boolean;
  /** Link each handle to that person's profile. A collective's browse list is
   *  the app's way into a contributor's library, so the handle is the way
   *  there; a list of one person's own sessions has nowhere to go. */
  linkOwner?: boolean;
}

export default function TranscriptList({
  items,
  showOwnerActions = false,
  title,
  headerAside,
  emptyState,
  bare = false,
  hideOwner = false,
  childSessions,
  facts = DEFAULT_TRANSCRIPT_ROW_FACTS,
  selection,
  viewerIsPrivileged = false,
  linkOwner = false,
}: TranscriptListProps) {
  const { user } = useAuth();
  const viewerId = user?.id;

  const rows =
    items.length > 0 ? (
      <div className="divide-y divide-rule">
        {items.map((item) => {
          const started = childSessions?.get(item.transcript.id);
          const carriesChip = started !== undefined && started.length > 0;
          const row = (
            <Row
              item={item}
              canManage={showOwnerActions && viewerId === item.owner.id}
              hideOwner={hideOwner}
              bottomSpace={carriesChip ? "tight" : "default"}
              facts={facts}
              selection={selection}
              viewerIsPrivileged={viewerIsPrivileged}
              linkOwner={linkOwner}
            />
          );
          // A row and the chip of sessions it started are ONE unit of the
          // divided list, so the rule falls between a parent and the next
          // parent rather than between a parent and its own chip.
          return !carriesChip ? (
            <div key={item.transcript.id}>{row}</div>
          ) : (
            <div key={item.transcript.id}>
              {row}
              <ChildSessionDisclosure
                parentTranscriptID={item.transcript.id}
                childSessions={started}
                showOwnerActions={showOwnerActions}
                hideOwner={hideOwner}
                facts={facts}
                selection={selection}
                viewerIsPrivileged={viewerIsPrivileged}
                linkOwner={linkOwner}
              />
            </div>
          );
        })}
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

/**
 * How much room a row leaves under itself.
 *
 * `default` is an ordinary row. `tight` is a row whose own chip of started
 * sessions follows it: at the full gap the chip reads as a break between two
 * separate things, rather than as one row and what hangs off it.
 *
 * A closed set rather than a boolean, so a third rhythm has to be named and
 * described here instead of arriving as a second flag to combine at each row.
 */
type RowBottomSpace = "default" | "tight";

/**
 * One fact drawn, or null when this row cannot state it.
 *
 * Returning null rather than a dash is deliberate: the line under a title reads
 * as a sentence about the session, and "— · — · 3 turns" states nothing three
 * times. A column in a table needs a placeholder because the column exists
 * whether or not the row fills it; a fact on a line does not.
 */
function factNode(fact: TranscriptRowFact, t: Transcript): React.ReactNode {
  switch (fact) {
    case "provider":
      return isHarness(t.model_provider) ? (
        <ProviderName harness={t.model_provider} />
      ) : (
        <Tag>{t.model_provider}</Tag>
      );
    case "model": {
      const name = formatModelName(t.model_name, t.model_provider);
      return name === "" ? null : (
        <span className="font-mono">{name}</span>
      );
    }
    case "date":
      return (
        <span className="font-mono tabular-nums">
          {new Date(t.published_at).toLocaleDateString("en-US", {
            month: "short",
            day: "numeric",
            year: "numeric",
          })}
        </span>
      );
    case "turns":
      return t.turn_count == null ? null : (
        <span className="font-mono tabular-nums">{t.turn_count} turns</span>
      );
    case "tokens": {
      // A transcript with no counted tokens at all states nothing rather than
      // claiming zero, which would read as a session that spent nothing.
      if (t.token_count == null && t.tokens_in == null && t.tokens_out == null) return null;
      return (
        <span className="font-mono tabular-nums">
          {formatCompact(transcriptTokens(t))} tok
        </span>
      );
    }
    case "branch":
      return t.git_branch == null || t.git_branch.trim() === "" ? null : (
        <span className="font-mono truncate" title={t.git_branch}>
          {t.git_branch}
        </span>
      );
    default:
      return assertFactExhaustive(fact);
  }
}

/** Compile-time proof that every {@link TranscriptRowFact} is drawn. Adding a
 *  member without a `case` above fails the BUILD rather than silently drawing
 *  nothing on every list that asks for it. */
function assertFactExhaustive(fact: never): never {
  throw new Error(`unhandled transcript row fact: ${String(fact)}`);
}

function Row({
  item,
  canManage,
  hideOwner,
  bottomSpace = "default",
  facts = DEFAULT_TRANSCRIPT_ROW_FACTS,
  selection,
  viewerIsPrivileged = false,
  linkOwner = false,
}: {
  item: TranscriptRow;
  canManage: boolean;
  hideOwner: boolean;
  bottomSpace?: RowBottomSpace;
  facts?: readonly TranscriptRowFact[];
  selection?: TranscriptRowSelection;
  viewerIsPrivileged?: boolean;
  linkOwner?: boolean;
}) {
  const { transcript: t, owner, shares } = item;
  const { user: viewer } = useAuth();
  const attribution = resolveAttribution(owner, viewer?.id, viewerIsPrivileged);
  const [editOpen, setEditOpen] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const del = useDeleteTranscript();

  const displayTitle = t.title || "Untitled";
  // Only the facts this row can actually state, paired with the fact that
  // produced each, so the separators fall between the ones that survive rather
  // than before each one.
  //
  // `facts` is an ordinary array and nothing forbids a caller repeating a
  // member, so the key below pairs the fact with its POSITION. Keying on the
  // fact alone would collide on a repeat.
  const statedFacts = facts
    .map((fact) => ({ fact, node: factNode(fact, t) }))
    .filter((stated) => stated.node !== null);
  const selectable = selection !== undefined;
  const selected = selection?.selectedIDs.has(t.id) ?? false;

  const ownerFace = attribution.anonymous ? (
    <div className="w-4 h-4 bg-surface-hover border border-rule flex items-center justify-center text-[8px] font-bold text-ink-3">
      ?
    </div>
  ) : owner.avatar_url ? (
    <img src={owner.avatar_url} alt="" className="w-4 h-4 border border-rule" />
  ) : (
    <div className="w-4 h-4 bg-surface-hover border border-rule flex items-center justify-center text-[8px] font-bold text-ink-2">
      {owner.github_username[0].toUpperCase()}
    </div>
  );
  // An anonymous author is never linked, whatever the list asked for: the whole
  // point of the anonymous pill is that it names nobody, and a link to
  // /users/<handle> would name them in its href.
  const ownerPill =
    linkOwner && !attribution.anonymous ? (
      <Link
        href={`/users/${encodeURIComponent(owner.github_username)}`}
        className="inline-flex items-center gap-2 focus-mono cursor-pointer hover:underline"
      >
        {ownerFace}
        <span className="text-xs text-ink-3">{attribution.label}</span>
      </Link>
    ) : (
      <>
        {ownerFace}
        <span className="text-xs text-ink-3">{attribution.label}</span>
      </>
    );

  function handleDelete(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    del.mutate(t.id);
  }

  return (
    <div
      className={cn(
        "group relative flex items-center gap-3 px-5 transition-colors hover:bg-surface-hover",
        // One design-system spacing step closer when the row's own chip follows
        // it. The chip's control cannot close the gap from its own side: it
        // carries a 44px minimum for its hit target, which its content does not
        // fill, so its padding is not what decides where its label sits.
        //
        // The top is restated at its ORIGINAL value rather than left to the
        // shorthand, so only the bottom moves and the row still opens the same
        // distance below the row above it.
        //
        // These two are design-system tokens where the rest of this file uses
        // Tailwind's own scale. The two scales are NOT interchangeable: `py-3`
        // and `var(--sp-3)` are both 12px, which is why the top can be restated
        // in either, but `px-5` here is 20px while `var(--sp-5)` is 24px. Do not
        // convert the rest of this row to tokens on the strength of these two.
        bottomSpace === "tight"
          ? "pt-[var(--sp-3)] pb-[var(--sp-2)]"
          : "py-3",
      )}
    >
      {/* The whole row is a link to the detail page, except for the action
          area on the right. The link is absolutely positioned over the row
          and the action cluster sits in front with z-index. */}
      <Link
        href={`/transcripts/${t.id}`}
        className="absolute inset-0 focus-mono cursor-pointer"
        aria-label={`Open transcript ${displayTitle}`}
      />

      {/* In front of the row-wide link, so picking a row out does not open it.
          A real checkbox input, so the box is reachable by keyboard and reports
          its own checked state, rather than a styled span that a screen reader
          reads as nothing. */}
      {selectable && (
        <label
          className="relative z-10 flex shrink-0 items-center justify-center"
          onClick={(e) => e.stopPropagation()}
        >
          <input
            type="checkbox"
            checked={selected}
            onChange={() => selection?.onToggle(t.id)}
            aria-label={`Select transcript ${displayTitle}`}
            className="size-3.5 cursor-pointer accent-[var(--mark)] focus-mono"
          />
        </label>
      )}

      <div className="min-w-0 flex-1 flex flex-col gap-0.5">
        <span className="text-sm text-ink font-medium truncate">
          {displayTitle}
        </span>
        <span className="text-[12px] text-ink-3 flex items-center gap-1.5 truncate">
          {/* The separator falls BETWEEN facts, so a row that cannot state one
              of them does not open with a stray mark or end with one. */}
          {statedFacts.map(({ fact, node }, i) => (
            <Fragment key={`${fact}-${i}`}>
              {i > 0 && <span className="text-rule">&middot;</span>}
              {node}
            </Fragment>
          ))}
          {/* The row says what it is wherever it appears, so a session that
              reached the page through a direct link or a collective list is
              labelled the same way it is inside the collapsed group. This is
              not a chosen fact: what a row IS cannot be turned off by the list
              it happens to sit in. */}
          {isAgentSession(t.session_origin) && (
            <>
              {statedFacts.length > 0 && <span className="text-rule">&middot;</span>}
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
        {!hideOwner && <>{ownerPill}</>}
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
