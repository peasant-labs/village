'use client';

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
// The demo's drop-in composite + its one wire to view adapter. This viewer
// mounts fairtrade's own graph engine and helpers directly; the demo and both
// apps all render the SAME composite.
import {
  TranscriptViewer,
  UnverifiedPullRequests,
  ToastProvider,
  useToast,
  PositionedLanding,
  usePositionedTranscript,
  type PositionedTranscript,
  type TurnLabel,
} from '@peasant-labs/fairtrade/ui';
import '@xyflow/react/dist/style.css';
import { SessionDetailV2TestId } from './readModeTestIds';
import { WholeDocumentGraph } from './WholeDocumentGraph';
import { TranscriptViewMode } from './transcriptViewMode';
import RequestFailureState from '@/components/RequestFailureState';
import type { SessionDetailPayload } from '@/types/messages';
import type { SessionRelationshipNavigation, UnverifiedPullRequestSource, VillageTranscriptIntegrity } from '@peasant-labs/schema';
import {
  readTranscriptReadState,
  writeTranscriptReadState,
} from '@/lib/transcriptReadState';
import {
  readCarriedRelationshipAnchor,
  relationshipHref,
  resolveRelationshipAnchorTurn,
} from '@/lib/relationshipAnchor';
import { useAuth } from '@/providers/AuthProvider';
import { useTheme } from '@/hooks/useTheme';
import {
  useTranscriptAnnotations,
  useCreateTranscriptAnnotation,
} from '@/lib/queries/transcripts';
import { buildSavedLabelsByEntry } from '@/lib/annotations';
import type { TranscriptContext } from '@/lib/transcriptContext';
import TranscriptEditDialog from '@/components/transcript/TranscriptEditDialog';
import TurnLabelPopover from '@/components/transcript/TurnLabelPopover';
import ContributePicker from '@/components/transcript/ContributePicker';
import TranscriptHeaderActions from '@/components/transcript/TranscriptHeaderActions';
import TranscriptPullRequests from '@/components/transcript/TranscriptPullRequests';
import ManageAccessDialog from '@/components/transcript/ManageAccessDialog';
import { useTranscriptCollectives } from '@/lib/queries/collectives';
import { useTranscriptPullRequests } from '@/lib/queries/pulls';
import {
  cookWholeTranscript,
  readTranscriptForDownload,
  readWholeTranscript,
  saveTranscriptFile,
  WholeTranscriptPurpose,
  type WholeTranscript,
  transcriptFile,
  type TranscriptExportFormat,
} from '@/lib/transcriptExport';
import { buildProjectHref, buildTranscriptBreadcrumb, shownTranscriptTitle } from './transcriptChrome';
import { TranscriptReadMode } from '@/lib/queries/segmentedReadCapability';
import { positionedReadTarget, villagePositionedTransport } from '@/lib/queries/positionedTransport';
import { isOwnedBy, type PublicOwner } from '@/lib/publicOwner';

/** A transcript's stored visibility. `shared` is set server-side when the
 *  transcript is shared to a collective; it is not directly selectable. */
type TranscriptVisibility = 'public' | 'private' | 'shared';

interface SessionDetailV2Props {
  sessionId: string;
  /** Backend transcript record id — the village identity of this page's own
   *  `/transcripts/<id>` route. Required: the one production caller
   *  (`app/transcripts/[id]/page.tsx`) always has it by the time this
   *  component renders past its loading/error states, and the copied link
   *  must be the real village id, never Peasant's local `sessionId`. */
  transcriptId: string;
  /** The transcript's real, stored visibility. */
  transcriptVisibility?: TranscriptVisibility;
  /** The transcript's stored title — drives the edit form's initial value. */
  transcriptTitle?: string | null;
  /** The transcript's stored description — drives the edit form's initial value. */
  transcriptDescription?: string | null;
  /** Owner user id — gates owner-only actions. */
  /** The transcript's owner as the server resolved them for this viewer. It
   *  decides whether the viewer owns the transcript and, with `projectHash`,
   *  builds the breadcrumb's project-page href. An anonymous owner yields
   *  neither. */
  owner?: PublicOwner | null;
  /** Where the transcript was opened from: the breadcrumb leads back there. */
  context?: TranscriptContext | null;
  /** The collective's name, for a collective context. */
  collectiveName?: string | null;
  /** The one server-resolved project display name (`Transcript.project_display_name`)
   *  — the single name every surface renders. */
  projectName: string | null;
  /** The transcript's `project_hash`, combined with `owner` to build
   *  the breadcrumb's project-page href. `null`/`undefined` degrades the
   *  crumb to a label with no link. */
  projectHash?: string | null;
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
  /** The detail response's `integrity`, present only when the server flagged
   *  this transcript for a reader allowed to see the flag. Handed to the
   *  viewer as served. */
  integrity?: VillageTranscriptIntegrity | null;
  /** The content route's `Village-Content-Hash` for the rendered `detail`.
   *  The viewer marks flagged turns only when it equals
   *  `transcriptContentHash`; village never compares them. */
  renderedContentHash?: string | null;
  error?: string | null;
  beforeHeader?: ReactNode;
  omittedImages?: number;
  publishedContent?: boolean;
  presentation?: 'ledger' | 'classic';
  /** `"full"` (default) is the standalone `/transcripts/{id}` route: every
   *  owner/viewer action, the trajectory graph, and the header action row.
   *  `"preview"` is the contribute page's read-only preview column: turns +
   *  tool calls only, a small header with a link to the full route, and
   *  every capability forced off (see the `variant === "preview"` branches
   *  below). Closed union; there is no third variant. */
  variant?: "full" | "preview";
  /** How the content is read, chosen by the page before this mounts.
   *  `content` (default) renders `detail`, the whole document. `positioned`
   *  mounts fairtrade's positioned reader over the transcript's segment
   *  routes and ignores `detail`; a failed positioned read shows the
   *  reader's own failure state and never falls back to the whole document. */
  readMode?: TranscriptReadMode;
  /** The harness the metadata read names; a download parses with it. */
  knownHarness?: string;
}

/** The two ways the view is mounted: the standalone route, or a preview column. */
const SessionDetailVariant = { Full: "full", Preview: "preview" } as const satisfies Record<string, NonNullable<SessionDetailV2Props["variant"]>>;

export { SessionDetailV2TestId };

type ContentProps = SessionDetailV2Props & { positioned?: PositionedTranscript };

/**
 * Thin adapter around fairtrade's `<TranscriptViewer>` composite, the same
 * surface the design-system demo renders. Village owns the *app glue*: the
 * REST data layer (React Query `useTranscriptContent`), auth/ownership, the
 * header's copy-link and `more` actions with their dialogs, the pull request
 * list, and the downloads, and feeds the composite via props/callbacks; the
 * composite owns all rendering + view state. The
 * trajectory-graph engine is fairtrade's own `/graph` entry, mounted through
 * `graphSlot`.
 */
export function SessionDetailV2(props: SessionDetailV2Props) {
  return (
    <ToastProvider placement="top-right">
      {props.readMode === TranscriptReadMode.Positioned
        ? <PositionedSessionDetailV2 {...props} />
        : <SessionDetailV2Content {...props} />}
    </ToastProvider>
  );
}

/**
 * The positioned read: one reader per transcript and mount. The transport is
 * memoised on the transcript id, because the reader restarts whenever its
 * transport object changes. A `?turn=N` deep link asks for the window at that
 * recorded index and lands exactly on it when it is served; a carried branch
 * point goes through the locator.
 */
function PositionedSessionDetailV2(props: SessionDetailV2Props) {
  const { transcriptId, transcriptTitle, transcriptContentHash, variant } = props;
  const transport = useMemo(() => villagePositionedTransport(transcriptId), [transcriptId]);
  // A preview column sits on another page, whose query is not a deep link.
  const [target] = useState(() =>
    positionedReadTarget(
      transcriptId,
      variant === SessionDetailVariant.Preview || typeof window === 'undefined' ? '' : window.location.search,
      transcriptContentHash,
    ),
  );
  const session = useMemo(() => ({ title: shownTranscriptTitle(transcriptTitle) }), [transcriptTitle]);
  const positioned = usePositionedTranscript({ transport, target, landing: PositionedLanding.Exact, session });
  return <SessionDetailV2Content {...props} positioned={positioned} />;
}

/** Where the positioned page's one whole-document read for the graph stands. Closed. */
const GraphReadStatus = { Idle: 'idle', Ready: 'ready', Failed: 'failed' } as const;
type GraphRead =
  | { status: typeof GraphReadStatus.Idle }
  | { status: typeof GraphReadStatus.Ready; revision: string | undefined; whole: WholeTranscript }
  | { status: typeof GraphReadStatus.Failed; revision: string | undefined; message: string };

/** The positioned page's graph: the read in flight, its failure with a retry, or the graph. The
 *  list beside it stays positioned whatever happens here. */
function PositionedGraph({ read, onRetry }: { read: GraphRead; onRetry: () => void }) {
  if (read.status === GraphReadStatus.Ready) return <WholeDocumentGraph detail={read.whole.detail} viewModel={read.whole.viewModel} />;
  if (read.status === GraphReadStatus.Failed) {
    return (
      <RequestFailureState
        title="the trajectory graph could not load"
        message={read.message}
        retryLabel="retry the graph"
        onRetry={onRetry}
      />
    );
  }
  return (
    <p className="px-6 py-6 text-[length:var(--fs-body)] text-ink-3" role="status" data-testid={SessionDetailV2TestId.GraphLoading.wire}>
      loading the trajectory graph
    </p>
  );
}

/** The status line for a `?turn=N` the transcript does not serve. `requestedTurn` is the value the
 *  address carries; `openedLabel` is the label the opened row itself shows (the server's display
 *  number), or undefined when the row shows none. */
export function snappedTurnLine(requestedTurn: number, openedLabel: string | undefined): string {
  const opened = openedLabel ? `#${openedLabel}` : 'the first available turn';
  return `?turn=${requestedTurn} is not in this transcript's served view, so the viewer opened ${opened}.`;
}

type LoadedSegments = NonNullable<PositionedTranscript['viewerProps']['segments']>;

/** Every turn the positioned reader has loaded, in whichever collection shape it supplies. */
function loadedSegmentTurns(segments: LoadedSegments | undefined) {
  if (!segments) return [];
  const list = segments instanceof Map ? [...segments.values()] : Array.isArray(segments) ? segments : Object.values(segments);
  return list.flatMap((segment) => segment.turns);
}

function SessionDetailV2Content({
  sessionId,
  transcriptId,
  transcriptVisibility,
  transcriptTitle,
  transcriptDescription,
  owner,
  context,
  collectiveName,
  projectName,
  projectHash,
  detail,
  relationshipNavigation,
  transcriptContentHash,
  integrity,
  renderedContentHash,
  error,
  beforeHeader,
  omittedImages = 0,
  publishedContent = false,
  presentation,
  variant = "full",
  knownHarness,
  positioned,
}: ContentProps) {
  const toast = useToast();
  void sessionId; // retained in the prop contract for callers / deep links
  const { user } = useAuth();
  const { theme } = useTheme();
  const isPreview = variant === "preview";
  // A split-pane preview starts with room for the transcript. Explicit rail
  // toggles stay available; the standalone reader keeps its own defaults.
  const [previewSidebarLayout, setPreviewSidebarLayout] = useState({ outline: false, filters: false, host: true });
  const isOwner = !isPreview && isOwnedBy(owner, user?.id);

  const router = useRouter();
  // Read the persisted per-transcript read state once; the state initializers
  // below seed from it, so Back after following a source link restores the
  // child's query, selection, disclosure, and scroll.
  const [restoredReadState] = useState(() => readTranscriptReadState(transcriptId));
  const [sourcePosition, setSourcePosition] = useState<(UnverifiedPullRequestSource & { requestKey: number }) | null>(null);

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
  const [contributeOpen, setContributeOpen] = useState(false);

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

  // The ONE wire→view projection (fairtrade's adapter). Village has no
  // personal-medians stream; the scorecard rides when the payload carries one.
  const vm = useMemo(
    () => (detail
      ? cookWholeTranscript(detail, { storedTitle: transcriptTitle, relationshipNavigation, published: publishedContent })
      : null),
    [detail, transcriptTitle, relationshipNavigation, publishedContent],
  );

  // Resolve the carried anchor against the COOKED target turns (the adapter's
  // rendered mapping, including folded call refs). Absent/unresolvable stays
  // null: the target opens at its ordinary position.
  // A positioned read resolves the carried anchor through its locator.
  const isPositioned = positioned !== undefined;
  const anchorTurn = useMemo(
    () => (!isPositioned && carriedAnchor && vm ? resolveRelationshipAnchorTurn(vm.turns, carriedAnchor) : null),
    [isPositioned, carriedAnchor, vm],
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
    // A positioned read lands where the reader's window says; a saved
    // position from another read is not a served position.
    () => (positioned ? undefined : initialTurnParam ?? restoredReadState.activeTurn ?? undefined),
  );
  const [search, setSearch] = useState<string>(restoredReadState.search);
  const [earlierHistoryOpen, setEarlierHistoryOpen] = useState<Record<string, boolean>>(
    () => (positioned ? {} : restoredReadState.earlierHistoryOpen),
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

  // The transcript's own village link: the copy button copies it and the
  // markdown download names it. The header renders only
  // once the content has loaded in the browser, so the origin is known there.
  const transcriptUrl =
    typeof window === 'undefined'
      ? `/transcripts/${transcriptId}`
      : `${window.location.origin}/transcripts/${transcriptId}`;

  // Every download, from the header's `download markdown` row and from the
  // raw json/jsonl rows, is one file written from what the page shows.
  // A positioned page holds only part of the transcript, so its downloads
  // read the whole stored copy, once per download, and save it only while the
  // page still shows the same revision of the same transcript.
  const positionedRevision = positioned?.controls.header?.revision;
  const downloadEpoch = useRef({ id: 0, abort: new AbortController() });
  useEffect(() => {
    const epoch = downloadEpoch.current;
    return () => {
      epoch.abort.abort();
      downloadEpoch.current = { id: epoch.id + 1, abort: new AbortController() };
    };
  }, [transcriptId, positionedRevision]);
  // The positioned page draws the trajectory graph from the whole stored copy,
  // read once when the reader opens the graph: a reader action like a
  // download, never an answer to a positioned failure. The list never reads
  // it. The read carries the download's epoch, so a revision change or an
  // unmount aborts it and discards its answer.
  const [viewMode, setViewMode] = useState<TranscriptViewMode>(TranscriptViewMode.List);
  const [graphRead, setGraphRead] = useState<GraphRead>({ status: GraphReadStatus.Idle });
  const graphCurrent: GraphRead =
    graphRead.status !== GraphReadStatus.Idle && graphRead.revision === positionedRevision ? graphRead : { status: GraphReadStatus.Idle };
  const graphWanted = positioned !== undefined && viewMode === TranscriptViewMode.Graph;
  // The revision a read is in flight for; a second render never starts a second read.
  const graphRequested = useRef<{ revision: string | undefined } | null>(null);
  useEffect(() => {
    if (!graphWanted || graphCurrent.status !== GraphReadStatus.Idle) return;
    const revision = positionedRevision;
    if (graphRequested.current && graphRequested.current.revision === revision) return;
    graphRequested.current = { revision };
    const { id, abort } = downloadEpoch.current;
    const epoch = { signal: abort.signal, isCurrent: () => downloadEpoch.current.id === id };
    readWholeTranscript({ transcriptId, storedTitle: transcriptTitle, knownHarness, relationshipNavigation }, epoch, WholeTranscriptPurpose.Graph).then(
      (whole) => {
        if (whole && epoch.isCurrent()) setGraphRead({ status: GraphReadStatus.Ready, revision, whole });
      },
      (reason: unknown) => {
        if (!epoch.isCurrent()) return;
        setGraphRead({
          status: GraphReadStatus.Failed,
          revision,
          message: reason instanceof Error ? reason.message : 'the trajectory graph could not be read. try again.',
        });
      },
    );
  }, [graphWanted, graphCurrent.status, positionedRevision, transcriptId, transcriptTitle, knownHarness, relationshipNavigation]);

  function handleExport(format: TranscriptExportFormat) {
    if (positioned) {
      const { id, abort } = downloadEpoch.current;
      readTranscriptForDownload(
        { transcriptId, format, url: transcriptUrl, storedTitle: transcriptTitle, knownHarness, relationshipNavigation },
        { signal: abort.signal, isCurrent: () => downloadEpoch.current.id === id },
      ).catch((reason: unknown) => {
        toast.err(reason instanceof Error ? reason.message : 'the transcript download could not be read. try the download again.', { title: 'download failed' });
      });
      return;
    }
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

  if (!detail && !positioned) {
    // A REST failure with nothing loaded must be VISIBLE — an endless
    // shimmer with the error swallowed is how a dead backend hides.
    if (error) {
      return (
        <div className="max-w-[1600px] mx-auto px-6 pt-6">
          <div className="border border-danger/40 bg-danger-soft px-4 py-3 text-sm text-danger">
            <p className="font-medium">transcript unavailable</p>
            <p className="mt-1 text-[length:var(--fs-body)]">{error}</p>
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
  // What the viewer reads: the positioned reader's own props, or the whole
  // document cooked here. The host keeps its own active-turn state either way.
  const positionedProps = positioned?.viewerProps;
  const readProps = positionedProps
    ? {
        ...positionedProps,
        // `usePositionedTranscript` always supplies the view model; its
        // published type is a partial of the viewer's props.
        viewModel: positionedProps.viewModel!,
        initialPosition: sourcePosition != null
          ? { kind: 'tool-call' as const, ...sourcePosition, requestKey: String(sourcePosition.requestKey) }
          : positionedProps.initialPosition,
        onActiveTurnChange: (turn: number) => {
          setActiveTurn(turn);
          positionedProps.onActiveTurnChange?.(turn);
        },
      }
    : {
        viewModel: vm!,
        integrity,
        transcriptContentHash,
        contentHash: renderedContentHash,
        initialPosition:
          sourcePosition != null
            ? { kind: 'tool-call' as const, ...sourcePosition, requestKey: String(sourcePosition.requestKey) }
            : anchorTurn != null
            ? { kind: 'turn' as const, turnIndex: anchorTurn, requestKey: carriedAnchor?.sourceEntryRef }
            : undefined,
        onActiveTurnChange: setActiveTurn,
        earlierHistoryOpen,
        onEarlierHistoryOpenChange: setEarlierHistoryOpen,
      };
  const landing = positioned?.controls.landing;
  // The opened row names itself by the label the server gave it; the host never derives a
  // number from the recorded index. The line waits until that row is loaded.
  const openedRow =
    landing?.snapped && landing.anchorTurn !== undefined
      ? loadedSegmentTurns(positionedProps?.segments).find((turn) => turn.recordedIndex === landing.anchorTurn)
      : undefined;
  const snappedLine =
    openedRow && landing?.requestedAnchor !== undefined
      ? snappedTurnLine(landing.requestedAnchor, openedRow.numberAbsent ? undefined : openedRow.label)
      : null;
  const shownOmittedImages = positioned ? positioned.controls.header?.omittedImages ?? 0 : omittedImages;
  const readMode = positioned ? TranscriptReadMode.Positioned : TranscriptReadMode.Content;

  const project = projectName;
  const projectHref = buildProjectHref(owner, projectHash);
  const visibility = transcriptVisibility ?? 'private';

  return (
    // Bounded host: the composite's .txn-app is height:100%, so a bounded
    // column is what lets its stream scroll internally — which is what
    // reveals the sticky scrubber timeline and anchors the keybind hint.
    <div
      className={isPreview ? 'on-transcript-preview' : 'flex flex-col h-full min-h-0 min-w-0'}
      data-transcript-mode={readMode}
      data-testid={(positioned ? SessionDetailV2TestId.ModePositioned : SessionDetailV2TestId.ModeContent).wire}
    >
      <div className="flex-1 min-h-0 min-w-0" ref={streamContainerRef}>
        <TranscriptViewer
          {...readProps}
          beforeHeader={<>
      {beforeHeader}
      {/* Above the header, not in the stream: landing scrolls the stream to
          the opened row, which would carry a line at its top out of view. */}
      {snappedLine && (
        <p
          role="status"
          className="max-w-[1600px] w-full mx-auto px-6 pt-2 text-[length:var(--fs-body)] text-ink-3"
          data-testid={SessionDetailV2TestId.SnappedTurn.wire}
        >
          {snappedLine}
        </p>
      )}
      {shownOmittedImages > 0 && (
        <p className="max-w-[1600px] w-full mx-auto px-6 pt-2 text-[length:var(--fs-body)] text-ink-3">
          {shownOmittedImages} {shownOmittedImages === 1 ? 'image' : 'images'} omitted
        </p>
      )}
      {error != null && (
        <p className="max-w-[1600px] w-full mx-auto px-6 pt-2 text-[length:var(--fs-body)] text-danger shrink-0">
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
              className="text-[length:var(--fs-body)] text-ink-3 hover:text-ink transition-colors focus-mono shrink-0"
              data-testid="preview-open-full-link"
            >
              open full transcript
            </Link>
          )}
        </div>
      )}
          </>}
          // The standalone route is the whole page, so its title is the page's
          // h1; the contribute page's preview column sits under that page's own.
          headingLevel={isPreview ? 2 : 1}
          presentation={presentation}
          sidebarLayout={isPreview ? previewSidebarLayout : undefined}
          onSidebarLayoutChange={isPreview ? setPreviewSidebarLayout : undefined}
          theme={theme}
          // The header row is the host's own, as in the fairtrade in-use demo:
          // the link, an icon-only copy button and a `more` menu. The
          // composite's share/more tail is off, and so is the outcome chip.
          // The owner's `more` menu adds `manage access` (who can read it,
          // which used to sit in this row as chips) and `edit title`; every
          // reader can download the markdown. The preview column shows none
          // of it.
          utilityActions={
            isPreview ? undefined : (
              <span className="inline-flex items-center gap-2">
                {transcriptId && (
                  <TranscriptHeaderActions
                    url={transcriptUrl}
                    onCopied={() => toast.ok('transcript link copied.', { title: 'copied' })}
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
          // The footer's shortcut hint and position counter stay available.
          footer={{ hints: true, progress: true }}
          pullRequests={
            !isPreview && (pullRequestsQuery.isError || (pullRequestsQuery.data?.pull_requests?.length ?? 0) > 0 || (pullRequestsQuery.data?.unverified_pull_requests?.length ?? 0) > 0) ? (
              <>
              <UnverifiedPullRequests pullRequests={pullRequestsQuery.data?.unverified_pull_requests ?? []} onSourceSelect={(source) => setSourcePosition((previous) => ({ ...source, requestKey: (previous?.requestKey ?? 0) + 1 }))} />
              {(pullRequestsQuery.isError || (pullRequestsQuery.data?.pull_requests?.length ?? 0) > 0) && <TranscriptPullRequests
                pullRequests={pullRequestsQuery.data?.pull_requests ?? []}
                failed={pullRequestsQuery.isError}
                onRetry={() => void pullRequestsQuery.refetch()}
                // A pull request's page needs a session.
                linked={!!user}
              />}
              </>
            ) : undefined
          }
          // The composite tail is hidden. Header menus own downloads and
          // editing; the viewer retains only its mounted per-turn labels.
          capabilities={{
            canLabel,
            canEdit: false,
            canChangeVisibility: false,
            canContribute: !isPreview && !!user && !!transcriptId,
            canExport: false,
          }}
          callbacks={{
            // The contribute picker stays: it is the transcript page's way to
            // share this session to a collective, and nothing in the new header
            // replaces it.
            onContribute: isPreview ? undefined : () => setContributeOpen(true),
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
          breadcrumbContext={buildTranscriptBreadcrumb({
            project,
            projectHref,
            isLoggedIn: !!user,
            context,
            collectiveName,
          })}
          LinkComponent={Link}
          // The one-time position (a verified branch point, a deep link's
          // landing) and the earlier-history disclosures come in `readProps`.
          activeTurn={activeTurn}
          // Controlled read state the host persists, so Back after following a
          // source link restores the child's query and earlier disclosures.
          search={search}
          onSearchChange={setSearch}
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
          // A positioned page holds only some segments, so its graph draws
          // the whole stored copy it reads when the graph opens.
          graphSlot={
            isPreview
              ? undefined
              : positioned
              ? () => (
                  <PositionedGraph
                    read={graphCurrent}
                    onRetry={() => {
                      graphRequested.current = null;
                      setGraphRead({ status: GraphReadStatus.Idle });
                    }}
                  />
                )
              : detail && vm
              ? () => <WholeDocumentGraph detail={detail} viewModel={vm} />
              : undefined
          }
          viewMode={positioned ? viewMode : undefined}
          onViewModeChange={positioned ? setViewMode : undefined}
          // Village's typed label model — saved chips + the single-panel
          // popover both host-owned (the composite's good/neutral/bad model
          // stays the demo's).
          // Saved labels are keyed by the recorded index; a positioned row
          // carries its own, and a row with none has no labels.
          renderTurnActions={(turn) => {
            const entryIndex = positioned ? turn.recordedIndex : turn.index;
            return (
            <span className="inline-flex items-center gap-1.5">
              {(entryIndex === undefined ? [] : savedLabelsByEntry.get(entryIndex) ?? []).map((label) => (
                <span key={label.id || `${label.typeId}:${label.value}`} className="chip">
                  {label.value}
                </span>
              ))}
              {presentation === 'classic' && canLabel && transcriptId && entryIndex !== undefined && (
                <TurnLabelPopover
                  entryIndex={entryIndex}
                  onSave={(label) => handleLabelSave(label)}
                />
              )}
            </span>
            );
          }}
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

      {!isPreview && transcriptId && (
        <ContributePicker
          open={contributeOpen}
          onClose={() => setContributeOpen(false)}
          transcriptId={transcriptId}
          transcriptTitle={transcriptTitle ?? null}
          transcriptVisibility={visibility}
        />
      )}
    </div>
  );
}
