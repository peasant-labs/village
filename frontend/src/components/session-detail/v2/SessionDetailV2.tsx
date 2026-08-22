'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';
import { TrajectoryGraph, annotateTranscript, type TurnLabel } from '@peasant-labs/transcript-browser';
// The demo's drop-in composite + its one wire→view adapter. This viewer used
// to mount transcript-browser's <SessionDetail> — a sibling implementation of
// the same design that drifted from the demo with every DS change; the demo,
// the tb example, and (now) both apps all render the SAME composite.
import {
  TranscriptViewer,
  adaptTranscript,
  computeAnalytics,
} from '@peasant-labs/fairtrade/ui';
import '@xyflow/react/dist/style.css';
import type { SessionDetailPayload } from '@/types/messages';
import { detectPhases } from '@/lib/insights';
import { displayProject } from '@/lib/quality/utils';
import { navSections } from '@/lib/nav/sections';
import { useAuth } from '@/providers/AuthProvider';
import { useTheme } from '@/hooks/useTheme';
import {
  useUpdateTranscript,
  useTranscriptAnnotations,
  useCreateTranscriptAnnotation,
} from '@/lib/queries/transcripts';
import { buildSavedLabelsByEntry } from '@/lib/annotations';
import TranscriptEditDialog from '@/components/transcript/TranscriptEditDialog';
import ContributePicker from '@/components/transcript/ContributePicker';
import TurnLabelPopover from '@/components/transcript/TurnLabelPopover';
import AttestButton from '@/components/transcript/AttestButton';

/** A transcript's stored visibility. `shared` is set server-side when the
 *  transcript is shared to a collective; it is not directly selectable. */
type TranscriptVisibility = 'public' | 'private' | 'shared';

/** Same label the explore card (`TranscriptCard.tsx`) shows for a transcript
 *  with no stored title. The hero must show this — never the composite's own
 *  fallback chain, which derives a heading from the first `role: user` turn
 *  and can surface raw harness preamble (village#32). Exported so the
 *  dev-only visual harness (`app/dev/visual-harness/page.tsx`) mirrors the
 *  same literal instead of a second hardcoded copy. */
export const UNTITLED_TRANSCRIPT_LABEL = 'Untitled transcript';

/** Resolves the home section of the top nav (`frontend/src/lib/nav/sections.ts`) —
 *  the single source of truth for its label/href; the breadcrumb's root
 *  crumb reads from there instead of a second hardcoded copy. `explore` does
 *  not vary with auth state, so any `opts` value resolves the same section.
 *  Fails loudly rather than silently re-hardcoding a duplicate literal: if
 *  the `explore` section id is ever renamed or removed from the registry,
 *  this must surface as an actionable error, not a silent fallback to a
 *  stale copy of the label/href it exists to avoid duplicating. */
function findExploreSection(): ReturnType<typeof navSections>[number] {
  const found = navSections({ isLoggedIn: false }).find((s) => s.id === 'explore');
  if (!found) {
    throw new Error(
      "SessionDetailV2: nav/sections.ts has no 'explore' section — the breadcrumb's " +
        'root crumb has nothing to read its label/href from. Fix: keep an `explore` ' +
        'entry in navSections(), or update this reference if it was intentionally renamed.',
    );
  }
  return found;
}

/** The breadcrumb's root crumb, resolved once at module load. Exported so
 *  the dev-only visual harness renders the SAME crumb instead of a second
 *  registry lookup. */
export const EXPLORE_SECTION: ReturnType<typeof navSections>[number] = findExploreSection();

/** Breadcrumb crumbs share one line in a fixed-height trail. A stored title
 *  is frequently a whole sentence, so bound it the way fairtrade bounds the
 *  hero title (`TranscriptViewer.jsx`'s `rawTitle`/`cut` guard), at a
 *  shorter, breadcrumb-appropriate length. The `codePointAt` check keeps the
 *  cut from landing inside a surrogate pair (no mojibake before the
 *  ellipsis). Exported so the dev-only visual harness truncates the same way. */
export function truncateCrumbLabel(raw: string): string {
  const MAX = 60;
  if (raw.length <= MAX) return raw;
  const cut = (raw.codePointAt(MAX - 2) ?? 0) > 0xffff ? MAX - 2 : MAX - 1;
  return raw.slice(0, cut).trimEnd() + '…';
}

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
  projectName: string;
  detail: SessionDetailPayload | undefined;
  error?: string | null;
}

/**
 * Thin adapter around fairtrade's `<TranscriptViewer>` composite — the same
 * surface the design-system demo and the transcript-browser example render.
 * Village owns the *app glue* — the REST data layer (React Query
 * `useTranscriptContent`), auth/ownership, and the edit / contribute
 * mutations + dialogs — and feeds the composite via props/callbacks; the
 * composite owns all rendering + view state. transcript-browser remains only
 * as the trajectory-graph engine mounted through `graphSlot`.
 */
export function SessionDetailV2({
  sessionId,
  transcriptId,
  transcriptVisibility,
  transcriptTitle,
  transcriptDescription,
  transcriptOwnerId,
  projectName,
  detail,
  error,
}: SessionDetailV2Props) {
  void sessionId; // retained in the prop contract for callers / deep links
  const { user } = useAuth();
  const { theme } = useTheme();
  const isOwner = !!user && !!transcriptOwnerId && user.id === transcriptOwnerId;

  const updateTranscript = useUpdateTranscript();

  // Manual per-turn labels: fetch existing (GET) + persist new ones (POST).
  // Reachable here means the transcript is viewable, so labelling is gated on
  // a signed-in viewer with a backing transcript record. The composite never
  // reads auth — village decides who can label and supplies its own popover.
  const canLabel = !!user && !!transcriptId;
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
    // `SessionVM.title` is documented as "render-when-present; else the
    // consumer derives one from the first prompt". `adaptTranscript` never
    // sets it, so with nothing supplied here the hero fell back to the
    // composite's own derivation — the first `role: user` turn — which
    // surfaces raw harness preamble (e.g. `<local-command-caveat>Caveat: ...`)
    // as the headline whenever a session opens with tooling setup
    // (village#32). The explore card already shows the stored title, so the
    // hero must show the SAME title, not a re-derived one: always overlay
    // it, falling back to the explore card's own empty-title label rather
    // than letting the composite reach past it to the raw turn.
    return {
      ...adapted,
      session: {
        ...adapted.session,
        title: transcriptTitle?.trim() || UNTITLED_TRANSCRIPT_LABEL,
      },
    };
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

  const project = detail.project ?? projectName;
  const visibility = transcriptVisibility ?? 'private';
  // Neither detail.id nor sessionId is a village identity — both are
  // Peasant's LOCAL session id. The breadcrumb's id fallback must use the
  // real village transcript id (transcriptId, the id in this page's own
  // /transcripts/<id> URL), never fall back to the Peasant-side id.
  const trimmedTitle = transcriptTitle?.trim() ?? '';
  const shortVillageId = transcriptId.slice(0, 8);
  const crumbTitle = trimmedTitle ? truncateCrumbLabel(trimmedTitle) : shortVillageId;

  return (
    // Bounded host: the composite's .txn-app is height:100%, so a bounded
    // column is what lets its stream scroll internally — which is what
    // reveals the sticky scrubber timeline and anchors the keybind hint.
    <div className="flex flex-col" style={{ height: 'calc(100vh - 64px)' }}>
      {/* A fetch error after data loaded: the transcript on screen is the
          last good snapshot — say so instead of pretending it is live. */}
      {error != null && (
        <p className="max-w-[1600px] w-full mx-auto px-6 pt-2 text-[12px] text-danger shrink-0">
          connection error; showing the last loaded transcript.
        </p>
      )}
      <div className="flex-1 min-h-0">
        <TranscriptViewer
          viewModel={vm!}
          theme={theme}
          // Attestation leads the hero action row through the composite's
          // headerActions seam — its old strip above the viewer was an
          // awkward band with no demo equivalent. AttestButton self-gates on
          // having visible orgs; its popover floats from its own trigger.
          headerActions={
            user && transcriptId ? <AttestButton transcriptId={transcriptId} /> : undefined
          }
          // Capabilities gated by village auth/ownership; the composite's
          // canExport covers the download serializers (the old canDownload).
          capabilities={{
            canLabel,
            canEdit: isOwner && !!transcriptId,
            canChangeVisibility: isOwner && !!transcriptId,
            canContribute: !!user && !!transcriptId,
            canExport: true,
          }}
          callbacks={{
            onEdit: () => setEditOpen(true),
            onContribute: () => setContributeOpen(true),
            // The composite's visibility control just fires; the host flips
            // the stored value through village's update mutation.
            onChangeVisibility: () => {
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
          // Village has no /projects route: the hardcoded `/projects` and
          // `/projects/<project>` hrefs 404'd (village#33). Every crumb href
          // below must resolve to a route that actually exists. The project
          // label still carries meaning (Peasant's privacy-safe project
          // label) even with no village route to link it to, so it stays as
          // a label-only crumb rather than being dropped. The last crumb
          // reads the RAW stored title (trimmed, truncated) — not the
          // overlaid hero fallback above — so an untitled transcript shows
          // the short VILLAGE transcript id instead of repeating
          // "Untitled transcript" three words wide in a breadcrumb.
          breadcrumb={[
            { label: EXPLORE_SECTION.label, href: EXPLORE_SECTION.href },
            { label: displayProject(project) },
            { label: crumbTitle },
          ]}
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
          // The graph toggle mounts transcript-browser's @xyflow engine — the
          // one piece tb still owns (graph topology/pan/zoom; visuals are DS).
          graphSlot={() => (
            <TrajectoryGraph
              turns={turns}
              toolVMsByTurn={toolVMsByTurn}
              filteredTurns={turns}
              phases={phases}
              annotations={annotations}
              searchMatches={[]}
              provider={detail.harness}
            />
          )}
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

      {transcriptId && isOwner && (
        <TranscriptEditDialog
          open={editOpen}
          onClose={() => setEditOpen(false)}
          transcriptId={transcriptId}
          initialTitle={transcriptTitle ?? null}
          initialDescription={transcriptDescription ?? null}
          initialVisibility={visibility}
        />
      )}

      {transcriptId && (
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
