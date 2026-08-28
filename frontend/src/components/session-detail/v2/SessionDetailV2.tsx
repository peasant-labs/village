'use client';

import { useMemo, useState } from 'react';
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
import { detectPhases } from '@/lib/insights';
import { useAuth } from '@/providers/AuthProvider';
import { useTheme } from '@/hooks/useTheme';
import {
  useUpdateTranscript,
  useTranscriptAnnotations,
  useCreateTranscriptAnnotation,
} from '@/lib/queries/transcripts';
import { buildSavedLabelsByEntry } from '@/lib/annotations';
import { isAgentSession, type SessionOrigin } from '@/lib/sessionOrigin';
import TranscriptEditDialog from '@/components/transcript/TranscriptEditDialog';
import ContributePicker from '@/components/transcript/ContributePicker';
import TurnLabelPopover from '@/components/transcript/TurnLabelPopover';
import AttestButton from '@/components/transcript/AttestButton';
import TranscriptCollectives from '@/components/transcript/TranscriptCollectives';
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
 * REST data layer (React Query `useTranscriptContent`), auth/ownership, and
 * the edit / contribute mutations + dialogs, and feeds the composite via
 * props/callbacks; the composite owns all rendering + view state. The
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
  error,
  variant = "full",
}: SessionDetailV2Props) {
  void sessionId; // retained in the prop contract for callers / deep links
  const { user } = useAuth();
  const { theme } = useTheme();
  const isPreview = variant === "preview";
  const isOwner = !isPreview && !!user && !!transcriptOwnerId && user.id === transcriptOwnerId;

  const updateTranscript = useUpdateTranscript();

  // Manual per-turn labels: fetch existing (GET) + persist new ones (POST).
  // Reachable here means the transcript is viewable, so labelling is gated on
  // a signed-in viewer with a backing transcript record. The composite never
  // reads auth — village decides who can label and supplies its own popover.
  const canLabel = !isPreview && !!user && !!transcriptId;
  const annotationsQuery = useTranscriptAnnotations(transcriptId ?? '', !!transcriptId);
  const createAnnotation = useCreateTranscriptAnnotation();

  // Host-owned action dialogs, triggered by the viewer's callbacks.
  const [editOpen, setEditOpen] = useState(false);
  const [contributeOpen, setContributeOpen] = useState(false);

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
    );
    // Overlay the stored title onto the hero — see `overlayStoredTitle` for
    // why the composite's own derivation (falling through to the first
    // `role: user` turn) is not safe to let through unmodified (village#32).
    return overlayStoredTitle(adapted, transcriptTitle);
  }, [detail, turns, transcriptTitle]);

  // Cooked tool calls by turn index, fed into the graph engine's tool nodes.
  const toolVMsByTurn = useMemo(
    () => new Map((vm?.turns ?? []).map((turn) => [turn.index, turn.toolCalls])),
    [vm],
  );

  // ?turn=N permalinks land on that turn (read once on mount; afterwards the
  // composite reports position changes back).
  const [activeTurn, setActiveTurn] = useState<number | undefined>(() => {
    if (typeof window === 'undefined') return undefined;
    const raw = new URLSearchParams(window.location.search).get('turn');
    if (raw == null || raw === '') return undefined;
    const n = Number(raw);
    return Number.isInteger(n) && n >= 0 ? n : undefined;
  });

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
      <div className="flex-1 min-h-0">
        <TranscriptViewer
          viewModel={vm!}
          theme={theme}
          // Attestation leads the hero action row through the composite's
          // headerActions seam — its old strip above the viewer was an
          // awkward band with no demo equivalent. AttestButton self-gates on
          // having visible orgs; its popover floats from its own trigger.
          // The collectives holding this transcript sit here too. They are
          // shown to ANY viewer the server chose to show them to (the
          // endpoint is auth-optional and answers an empty list when the
          // visibility rule or the owner's contributor opt-in withholds
          // them), so the action row renders whenever there is a transcript
          // id, not only for a signed-in viewer. The preview variant hides
          // this whole row — collectives/attest are owner-facing actions
          // that do not belong in a read-only preview column.
          headerActions={
            !isPreview && (transcriptId || isAgentSession(sessionOrigin)) ? (
              <span className="inline-flex items-center gap-2">
                {isAgentSession(sessionOrigin) && (
                  <span className="chip" data-testid="agent-session-chip">
                    agent session
                  </span>
                )}
                {transcriptId && <TranscriptCollectives transcriptId={transcriptId} />}
                {user && transcriptId && <AttestButton transcriptId={transcriptId} />}
              </span>
            ) : undefined
          }
          // Capabilities gated by village auth/ownership; the composite's
          // canExport covers the download serializers (the old canDownload).
          // The preview variant forces every capability off: a viewer
          // previewing someone else's session in the contribute column must
          // never see or reach an owner-only action from inside the preview.
          capabilities={{
            canLabel,
            canEdit: !isPreview && isOwner && !!transcriptId,
            canChangeVisibility: !isPreview && isOwner && !!transcriptId,
            canContribute: !isPreview && !!user && !!transcriptId,
            canExport: !isPreview,
          }}
          callbacks={{
            onEdit: isPreview ? undefined : () => setEditOpen(true),
            onContribute: isPreview ? undefined : () => setContributeOpen(true),
            // The composite's visibility control just fires; the host flips
            // the stored value through village's update mutation.
            onChangeVisibility: isPreview
              ? undefined
              : () => {
                  if (!transcriptId) return;
                  updateTranscript.mutate({
                    id: transcriptId,
                    visibility: visibility === 'public' ? 'private' : 'public',
                  });
                },
            onCopyLink: () => {
              const url = transcriptId
                ? `${window.location.origin}/transcripts/${transcriptId}`
                : window.location.href;
              void navigator.clipboard?.writeText(url);
            },
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
            storedTitle: transcriptTitle,
            transcriptId,
          })}
          LinkComponent={Link}
          activeTurn={activeTurn}
          onActiveTurnChange={setActiveTurn}
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
