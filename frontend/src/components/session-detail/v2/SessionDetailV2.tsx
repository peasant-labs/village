'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { TrajectoryGraph } from '@peasant-labs/fairtrade/graph';
// The demo's drop-in composite + its one wire to view adapter. This viewer
// mounts fairtrade's own graph engine and helpers directly; the demo and both
// apps all render the SAME composite.
import {
  TranscriptViewer,
  adaptTranscript,
  computeAnalytics,
  annotateTranscript,
  type TurnLabel,
} from '@peasant-labs/fairtrade/ui';
import '@xyflow/react/dist/style.css';
import type { SessionDetailPayload } from '@/types/messages';
import type { SessionRelationshipNavigation } from '@peasant-labs/schema';
import {
  readTranscriptReadState,
  writeTranscriptReadState,
} from '@/lib/transcriptReadState';
import {
  readCarriedRelationshipAnchor,
  relationshipHref,
  resolveRelationshipAnchorTurn,
} from '@/lib/relationshipAnchor';
import { detectPhases } from '@/lib/insights';
import { useAuth } from '@/providers/AuthProvider';
import { useTheme } from '@/hooks/useTheme';
import {
  useTranscriptAnnotations,
  useCreateTranscriptAnnotation,
} from '@/lib/queries/transcripts';
import { buildSavedLabelsByEntry } from '@/lib/annotations';
import { isAgentSession, type SessionOrigin } from '@/lib/sessionOrigin';
import TranscriptEditDialog from '@/components/transcript/TranscriptEditDialog';
import TurnLabelPopover from '@/components/transcript/TurnLabelPopover';
import TranscriptHeaderActions from '@/components/transcript/TranscriptHeaderActions';
import TranscriptPullRequests from '@/components/transcript/TranscriptPullRequests';
import ManageAccessDialog from '@/components/transcript/ManageAccessDialog';
import { useTranscriptCollectives } from '@/lib/queries/collectives';
import { useTranscriptPullRequests } from '@/lib/queries/pulls';
import {
  saveTranscriptFile,
  transcriptFile,
  type TranscriptExportFormat,
} from '@/lib/transcriptExport';
import { buildProjectHref, buildTranscriptBreadcrumb, overlayStoredTitle } from './transcriptChrome';

/** A transcript's stored visibility. `shared` is set server-side when the
 *  transcript is shared to a collective; it is not directly selectable. */
type TranscriptVisibility = 'public' | 'private' | 'shared';

interface SessionDetailV2Props {
  sessionId: string;
  /** Backend transcript record id — the village identity of this page's own
   *  `/transcripts/<id>` route. Required: the one production caller
   *  (`app/transcripts/[id]/page.tsx`) always has it by the time this
   *  component renders past its loading/error states, and the breadcrumb's
   *  short-id fallback depends on it being the real village id, never
   *  Peasant's local `sessionId`. */
  transcriptId: string;
  /** The transcript's real, stored visibility. */
  transcriptVisibility?: TranscriptVisibility;
  /** The transcript's stored title — drives the edit form's initial value. */
  transcriptTitle?: string | null;
  /** The transcript's stored description — drives the edit form's initial value. */
  transcriptDescription?: string | null;
  /** Owner user id — gates owner-only actions. */
  transcriptOwnerId?: string;
  /** Who drove the session, as served by the API. An agent-driven session is
   *  labelled here so a viewer who arrived by direct link sees the same thing
   *  the collapsed list group told them. */
  sessionOrigin?: SessionOrigin;
  /** The one server-resolved project display name (`Transcript.project_display_name`)
   *  — the single name every surface renders. */
  projectName: string;
  /** The transcript's `project_hash`, combined with `ownerUsername` to build
   *  the breadcrumb's project-page href. `null`/`undefined` degrades the
   *  crumb to a label with no link. */
  projectHash?: string | null;
  /** The transcript owner's `github_username`, combined with `projectHash`
   *  to build the breadcrumb's project-page href. */
  ownerUsername?: string | null;
  detail: SessionDetailPayload | undefined;
  /** The viewer's authorized current-target navigation from the metadata read
   *  (`GET /transcripts/{id}`). It is read metadata, never durable content:
   *  it is handed to Fairtrade's one adapter, which cooks the source/starter
   *  links and any exact branch anchor. Absent when the metadata response
   *  carried no relationship link. */
  relationshipNavigation?: SessionRelationshipNavigation[];
  /** This transcript's current public representation revision
   *  (`Transcript.content_hash`, the digest of the redacted public bytes).
   *  A carried exact branch anchor is applied only while it still matches this
   *  value, the same authority the server used to emit the anchor. */
  transcriptContentHash?: string | null;
  error?: string | null;
  /** `"full"` (default) is the standalone `/transcripts/{id}` route: every
   *  owner/viewer action, the trajectory graph, and the header action row.
   *  `"preview"` is the contribute page's read-only preview column: turns +
   *  tool calls only, a small header with a link to the full route, and
   *  every capability forced off (see the `variant === "preview"` branches
   *  below). Closed union; there is no third variant. */
  variant?: "full" | "preview";
}

/**
 * Thin adapter around fairtrade's `<TranscriptViewer>` composite, the same
 * surface the design-system demo renders. Village owns the *app glue*: the
 * REST data layer (React Query `useTranscriptContent`), auth/ownership, the
 * header's link, copy and `more` actions with their dialogs, the pull request
 * list, and the downloads, and feeds the composite via props/callbacks; the
 * composite owns all rendering + view state. The
 * trajectory-graph engine is fairtrade's own `/graph` entry, mounted through
 * `graphSlot`.
 */
export function SessionDetailV2({
  sessionId,
  transcriptId,
  transcriptVisibility,
  transcriptTitle,
  transcriptDescription,
  transcriptOwnerId,
  sessionOrigin,
  projectName,
  projectHash,
  ownerUsername,
  detail,
  relationshipNavigation,
  transcriptContentHash,
  error,
  variant = "full",
}: SessionDetailV2Props) {
  void sessionId; // retained in the prop contract for callers / deep links
  const { user } = useAuth();
  const { theme } = useTheme();
  const isPreview = variant === "preview";
  const isOwner = !isPreview && !!user && !!transcriptOwnerId && user.id === transcriptOwnerId;

  const router = useRouter();
  // Read the persisted per-transcript read state once; the state initializers
  // below seed from it, so Back after following a source link restores the
  // child's query, selection, disclosure, and scroll.
  const [restoredReadState] = useState(() => readTranscriptReadState(transcriptId));

  // A source link may have carried the child's verified branch-point anchor
  // here (see `relationshipAnchor`). Capture the mount query once — it is a
  // one-time entry point, like `?turn=N` — and accept it only while this
  // target's current public revision still matches the carried revision.
  const [mountSearch] = useState(() =>
    typeof window === 'undefined' ? '' : window.location.search,
  );
  const carriedAnchor = useMemo(
    () => readCarriedRelationshipAnchor(mountSearch, transcriptContentHash),
    [mountSearch, transcriptContentHash],
  );

  // Manual per-turn labels: fetch existing (GET) + persist new ones (POST).
  // Reachable here means the transcript is viewable, so labelling is gated on
  // a signed-in viewer with a backing transcript record. The composite never
  // reads auth — village decides who can label and supplies its own popover.
  const canLabel = !isPreview && !!user && !!transcriptId;
  const annotationsQuery = useTranscriptAnnotations(transcriptId ?? '', !!transcriptId);
  const createAnnotation = useCreateTranscriptAnnotation();

  // Host-owned action dialogs, opened from the header's `more` menu.
  const [editOpen, setEditOpen] = useState(false);
  const [accessOpen, setAccessOpen] = useState(false);

  // Who can read it, for the owner's `manage access` row and popup. Nobody
  // else asks: the header shows the collectives to no one but the owner.
  const ownerCollectives = useTranscriptCollectives(isOwner ? transcriptId : '');
  const accessNames = useMemo(
    () => ownerCollectives.data?.map((c) => c.name),
    [ownerCollectives.data],
  );

  // The pull requests this transcript is bound to that this viewer may read.
  // The preview column shows none.
  const pullRequestsQuery = useTranscriptPullRequests(transcriptId, !isPreview);

  // Host-derived inputs for the GRAPH engine (the composite derives its own).
  const turns = useMemo(() => detail?.turns ?? [], [detail]);
  const phases = useMemo(() => detectPhases(turns), [turns]);
  const annotations = useMemo(
    () => annotateTranscript(turns),
    [turns],
  );

  // The ONE wire→view projection (fairtrade's adapter). Village has no
  // personal-medians stream; the scorecard rides when the payload carries one.
  const vm = useMemo(() => {
    if (!detail) return null;
    const analytics = computeAnalytics(turns as Parameters<typeof computeAnalytics>[0], {
      scorecard: detail.scorecard ?? undefined,
    } as Parameters<typeof computeAnalytics>[1]);
    const adapted = adaptTranscript(
      detail as Parameters<typeof adaptTranscript>[0],
      undefined,
      analytics,
      // Read metadata, not durable content: the adapter cooks the source/
      // starter links and an exact branch anchor only when the navigation it
      // receives still agrees with the durable relationship.
      { relationshipNavigation },
    );
    // Overlay the stored title onto the hero — see `overlayStoredTitle` for
    // why the composite's own derivation (falling through to the first
    // `role: user` turn) is not safe to let through unmodified (village#32).
    return overlayStoredTitle(adapted, transcriptTitle);
  }, [detail, turns, transcriptTitle, relationshipNavigation]);

  // Cooked tool calls by turn index, fed into the graph engine's tool nodes.
  const toolVMsByTurn = useMemo(
    () => new Map((vm?.turns ?? []).map((turn) => [turn.index, turn.toolCalls])),
    [vm],
  );

  // Resolve the carried anchor against the COOKED target turns (the adapter's
  // rendered mapping, including folded call refs). Absent/unresolvable stays
  // null: the target opens at its ordinary position.
  const anchorTurn = useMemo(
    () => (carriedAnchor && vm ? resolveRelationshipAnchorTurn(vm.turns, carriedAnchor) : null),
    [carriedAnchor, vm],
  );

  // ?turn=N permalinks land on that turn; otherwise the persisted read state
  // from a previous visit (Back after following a source link) is restored.
  // Afterwards the composite reports position changes back and the host
  // persists them, so browser Back returns to the same reading position.
  const [initialTurnParam] = useState<number | undefined>(() => {
    if (typeof window === 'undefined') return undefined;
    const raw = new URLSearchParams(window.location.search).get('turn');
    if (raw != null && raw !== '') {
      const n = Number(raw);
      if (Number.isInteger(n) && n >= 0) return n;
    }
    return undefined;
  });
  const [activeTurn, setActiveTurn] = useState<number | undefined>(
    () => initialTurnParam ?? restoredReadState.activeTurn ?? undefined,
  );
  const [search, setSearch] = useState<string>(restoredReadState.search);
  const [earlierHistoryOpen, setEarlierHistoryOpen] = useState<Record<string, boolean>>(
    restoredReadState.earlierHistoryOpen,
  );

  // The verified branch point wins over `?turn=N` and any previously saved
  // position for this target: the reader asked to be shown where the child
  // branched. The viewer's `initialPosition` below performs the one-time
  // scroll; this keeps the turn selected (and persisted) afterwards. Adjusting
  // state during render (not in an effect) is the derived-state pattern: it
  // applies once per resolved anchor and re-renders immediately.
  const [appliedAnchorTurn, setAppliedAnchorTurn] = useState<number | null>(null);
  if (anchorTurn != null && anchorTurn !== appliedAnchorTurn) {
    setAppliedAnchorTurn(anchorTurn);
    setActiveTurn(anchorTurn);
  }

  // The scroller the host restores after Back. Fairtrade owns the stream's own
  // scroll behavior; village owns where the reader was.
  const streamContainerRef = useRef<HTMLDivElement | null>(null);
  const scrollTopRef = useRef<number>(restoredReadState.scrollTop);
  const readStateRef = useRef({ search, activeTurn, earlierHistoryOpen });
  useEffect(() => {
    readStateRef.current = { search, activeTurn, earlierHistoryOpen };
  }, [search, activeTurn, earlierHistoryOpen]);

  const persistReadState = useCallback(() => {
    writeTranscriptReadState(transcriptId, {
      search: readStateRef.current.search,
      activeTurn: readStateRef.current.activeTurn ?? null,
      earlierHistoryOpen: readStateRef.current.earlierHistoryOpen,
      scrollTop: scrollTopRef.current,
    });
  }, [transcriptId]);

  // Persist the latest reading position when the child route unmounts (e.g.
  // navigating to the current parent) so Back lands where it left.
  useEffect(() => () => persistReadState(), [persistReadState]);

  useEffect(() => {
    if (!detail) return;
    const stream = streamContainerRef.current?.querySelector<HTMLElement>('.txn-stream');
    if (!stream) return;
    // A one-time position — the `?turn=N` permalink or the carried branch
    // anchor — owns the initial scroll. Only when neither is present does the
    // saved read position apply; otherwise this restore would immediately undo
    // the composite's own `initialPosition` scroll.
    const hasInitialPosition = initialTurnParam != null || anchorTurn != null;
    if (!hasInitialPosition) stream.scrollTop = scrollTopRef.current;
    const onScroll = () => {
      scrollTopRef.current = stream.scrollTop;
      persistReadState();
    };
    stream.addEventListener('scroll', onScroll, { passive: true });
    return () => stream.removeEventListener('scroll', onScroll);
  }, [detail, persistReadState, initialTurnParam, anchorTurn]);

  // A source/starter link opens the CURRENT target, never a historical replay.
  // The adapter only reports a usable navigation target when the viewer is
  // authorized; village owns the route and the Back restoration above. When
  // the navigation carries a verified exact branch point, the route carries it
  // too, so the target opens there instead of at its default position.
  function handleNavigateRelationship(navigation: SessionRelationshipNavigation) {
    persistReadState();
    const href = relationshipHref(navigation);
    if (href) router.push(href);
  }

  // The transcript's own village link: the header shows it, the copy button
  // copies it, and the markdown download names it. The header renders only
  // once the content has loaded in the browser, so the origin is known there.
  const transcriptUrl =
    typeof window === 'undefined'
      ? `/transcripts/${transcriptId}`
      : `${window.location.origin}/transcripts/${transcriptId}`;

  // Every download, from the header's `download markdown` row and from the
  // raw json/jsonl rows, is one file written from what the page shows.
  function handleExport(format: TranscriptExportFormat) {
    if (!vm || !detail) return;
    saveTranscriptFile(
      transcriptFile(format, { viewModel: vm, detail, url: transcriptUrl, transcriptId }),
    );
  }

  // Existing saved labels → chips rendered in the host's per-turn actions.
  const savedLabelsByEntry = useMemo(
    () => buildSavedLabelsByEntry(annotationsQuery.data?.annotations ?? []),
    [annotationsQuery.data],
  );

  // POST a manual label; the chips refresh through the annotations query.
  async function handleLabelSave(label: TurnLabel): Promise<TurnLabel> {
    if (!transcriptId) return label;
    const created = await createAnnotation.mutateAsync({
      transcriptId,
      typeId: label.typeId,
      value: label.value,
      entryIndex: label.entryIndex,
    });
    return { ...label, id: created.id };
  }

  if (!detail) {
    // A REST failure with nothing loaded must be VISIBLE — an endless
    // shimmer with the error swallowed is how a dead backend hides.
    if (error) {
      return (
        <div className="max-w-[1600px] mx-auto px-6 pt-6">
          <div className="border border-danger/40 bg-danger-soft px-4 py-3 text-sm text-danger">
            <p className="font-medium">transcript unavailable</p>
            <p className="mt-1 text-[13px]">{error}</p>
          </div>
        </div>
      );
    }
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12">
        <div className="flex flex-col gap-3">
          <div className="h-4 w-1/3 animate-shimmer" />
          <div className="h-9 w-2/3 animate-shimmer" />
          <div className="h-6 w-1/2 animate-shimmer" />
        </div>
        <div className="mt-6 h-[60vh] w-full animate-shimmer" />
      </div>
    );
  }

  // The one server-resolved project name every surface renders.
  // `detail.project` is the content endpoint's own raw wire field and is NOT
  // the resolved identity — reading it here would reintroduce the "two
  // algorithms disagree" bug this slice removes, so `projectName` always
  // wins.
  const project = projectName;
  const projectHref = buildProjectHref(ownerUsername, projectHash);
  const visibility = transcriptVisibility ?? 'private';

  return (
    // Bounded host: the composite's .txn-app is height:100%, so a bounded
    // column is what lets its stream scroll internally — which is what
    // reveals the sticky scrubber timeline and anchors the keybind hint.
    <div className="flex flex-col h-[calc(100dvh-var(--app-header-height))]">
      {/* A fetch error after data loaded: the transcript on screen is the
          last good snapshot — say so instead of pretending it is live. */}
      {error != null && (
        <p className="max-w-[1600px] w-full mx-auto px-6 pt-2 text-[12px] text-danger shrink-0">
          connection error; showing the last loaded transcript.
        </p>
      )}
      {isPreview && (
        // The preview column's own small header: title + a link to the full
        // route. Everything else the full route's header row carries
        // (collectives, attest, breadcrumb project link) is owner/viewer
        // chrome that does not belong in a read-only preview.
        <div
          className="flex items-center justify-between gap-3 px-4 py-2 border-b border-rule shrink-0"
          data-testid="preview-header"
        >
          <span className="text-sm text-ink truncate">
            {transcriptTitle || 'untitled transcript'}
          </span>
          {transcriptId && (
            <Link
              href={`/transcripts/${transcriptId}`}
              className="text-[12px] text-ink-3 hover:text-ink transition-colors focus-mono shrink-0"
              data-testid="preview-open-full-link"
            >
              open full transcript
            </Link>
          )}
        </div>
      )}
      <div className="flex-1 min-h-0" ref={streamContainerRef}>
        <TranscriptViewer
          viewModel={vm!}
          theme={theme}
          // The header row is the host's own, as in the fairtrade in-use demo:
          // the link, an icon-only copy button and a `more` menu. The
          // composite's share/more tail is off, and so is the outcome chip.
          // The owner's `more` menu adds `manage access` (who can read it,
          // which used to sit in this row as chips) and `edit title`; every
          // reader can download the markdown. The preview column shows none
          // of it.
          headerActions={
            isPreview ? undefined : (
              <span className="inline-flex items-center gap-2">
                {isAgentSession(sessionOrigin) && (
                  <span className="chip" data-testid="agent-session-chip">
                    agent session
                  </span>
                )}
                {transcriptId && (
                  <TranscriptHeaderActions
                    url={transcriptUrl}
                    owner={
                      isOwner
                        ? {
                            accessNames,
                            visibility,
                            onManageAccess: () => setAccessOpen(true),
                            onEditTitle: () => setEditOpen(true),
                          }
                        : undefined
                    }
                    onDownloadMarkdown={() => handleExport('markdown')}
                    onDownloadJSON={() => handleExport('json')}
                    onDownloadJSONL={() => handleExport('jsonl')}
                  />
                )}
              </span>
            )
          }
          showTail={false}
          showOutcome={false}
          pullRequests={
            !isPreview && (pullRequestsQuery.isError || (pullRequestsQuery.data?.length ?? 0) > 0) ? (
              <TranscriptPullRequests
                pullRequests={pullRequestsQuery.data ?? []}
                failed={pullRequestsQuery.isError}
                onRetry={() => void pullRequestsQuery.refetch()}
              />
            ) : undefined
          }
          // The composite tail is hidden. Header menus own downloads and
          // editing; the viewer retains only its mounted per-turn labels.
          capabilities={{
            canLabel,
            canEdit: false,
            canChangeVisibility: false,
            canContribute: false,
            canExport: false,
          }}
          callbacks={{
            // Village owns the route to the current source/starter target and
            // the read-state restoration on Back. The adapter only reports a
            // target the viewer is authorized to open.
            onNavigateRelationship: isPreview ? undefined : handleNavigateRelationship,
          }}
          // Village's host trail through the app router (lowercase chrome).
          // The project crumb links to `/users/{username}/projects/{hash}`
          // when both the owner's username and the project hash are
          // known; otherwise it degrades to a label-only crumb rather than
          // emitting a broken link. The last crumb reads the RAW stored
          // title (trimmed, truncated) — not the overlaid hero fallback
          // above — so an untitled transcript shows the short VILLAGE
          // transcript id instead of repeating "Untitled transcript" three
          // words wide in a breadcrumb.
          breadcrumb={buildTranscriptBreadcrumb({
            project,
            projectHref,
            isLoggedIn: !!user,
            storedTitle: transcriptTitle,
            transcriptId,
          })}
          LinkComponent={Link}
          // A verified branch point opens the target there (one-time position);
          // without one the existing `?turn=N` / saved-position behavior runs.
          initialPosition={
            anchorTurn != null
              ? { kind: 'turn', turnIndex: anchorTurn, requestKey: carriedAnchor?.sourceEntryRef }
              : undefined
          }
          activeTurn={activeTurn}
          onActiveTurnChange={setActiveTurn}
          // Controlled read state the host persists, so Back after following a
          // source link restores the child's query and earlier disclosures.
          search={search}
          onSearchChange={setSearch}
          earlierHistoryOpen={earlierHistoryOpen}
          onEarlierHistoryOpenChange={setEarlierHistoryOpen}
          // Per-turn copied anchors are full permalinks into the transcript
          // record — the pre-composite link shape.
          anchorHref={(turnIndex) =>
            transcriptId
              ? `/transcripts/${transcriptId}?turn=${turnIndex}`
              : `#turn-${turnIndex}`
          }
          // The graph toggle mounts fairtrade's @xyflow engine (graph
          // topology/pan/zoom; node visuals are the design-system's own).
          // The preview variant omits this render-prop entirely so the
          // trajectory graph is never mounted there. The composite's own
          // list/graph toggle is not conditioned on graphSlot, so it still
          // renders in preview and shows the composite's built-in "no graph
          // engine" empty state; hiding the toggle itself is a fairtrade
          // follow-up.
          graphSlot={
            isPreview
              ? undefined
              : () => (
                  <TrajectoryGraph
                    turns={turns}
                    toolVMsByTurn={toolVMsByTurn}
                    filteredTurns={turns}
                    phases={phases}
                    annotations={annotations}
                    searchMatches={[]}
                    provider={detail.harness}
                  />
                )
          }
          // Village's typed label model — saved chips + the single-panel
          // popover both host-owned (the composite's good/neutral/bad model
          // stays the demo's).
          renderTurnActions={(turn) => (
            <span className="inline-flex items-center gap-1.5">
              {(savedLabelsByEntry.get(turn.index) ?? []).map((label) => (
                <span key={label.id || `${label.typeId}:${label.value}`} className="chip">
                  {label.value}
                </span>
              ))}
              {canLabel && transcriptId && (
                <TurnLabelPopover
                  entryIndex={turn.index}
                  onSave={(label) => handleLabelSave(label)}
                />
              )}
            </span>
          )}
        />
      </div>

      {!isPreview && transcriptId && isOwner && (
        <TranscriptEditDialog
          open={editOpen}
          onClose={() => setEditOpen(false)}
          transcriptId={transcriptId}
          initialTitle={transcriptTitle ?? null}
          initialDescription={transcriptDescription ?? null}
          initialVisibility={visibility}
        />
      )}

      {!isPreview && transcriptId && isOwner && accessOpen && (
        <ManageAccessDialog
          open={accessOpen}
          onClose={() => setAccessOpen(false)}
          transcriptId={transcriptId}
        />
      )}
    </div>
  );
}
