"use client";

import { useEffect, useMemo, useState } from "react";
import { notFound } from "next/navigation";
import { TrajectoryGraph } from "@peasant-labs/fairtrade/graph";
// The harness mounts what production mounts: fairtrade's TranscriptViewer
// composite (the same surface the demo renders).
import {
  TranscriptViewer,
  adaptTranscript,
  computeAnalytics,
  annotateTranscript,
  type TurnLabel,
} from "@peasant-labs/fairtrade/ui";
import "@xyflow/react/dist/style.css";
import { Moon, Sun } from "lucide-react";
import { detectPhases } from "@/lib/insights";
import TurnLabelPopover from "@/components/transcript/TurnLabelPopover";
import {
  buildProjectHref,
  buildTranscriptBreadcrumb,
  overlayStoredTitle,
} from "@/components/session-detail/v2/transcriptChrome";
import { sampleSession } from "./sample-session";

type Theme = "dark" | "light";

/**
 * The harness fixture is a bare `SessionDetailPayload` with no backing
 * transcript metadata row, so it has no real `transcriptTitle`/`transcriptId`
 * the way the production route's REST fetch does. This constant stands in
 * for that missing metadata — the SAME input `SessionDetailV2` reads to
 * overlay the hero title and build the breadcrumb's last crumb — so the
 * harness capture proves the same production behavior via the shared
 * `transcriptChrome` builders instead of a hand-copied second rule.
 */
const SAMPLE_TRANSCRIPT_TITLE = "Port the transcript canvas into the shared package";
/** Stands in for the village transcript id (never Peasant's local
 *  `sampleSession.id`) — mirrors `transcriptId` in `SessionDetailV2`, fed
 *  into `buildTranscriptBreadcrumb`'s short-id fallback parameter below. */
const SAMPLE_TRANSCRIPT_ID = "transcript-sess-demo-0001";
/** Stands in for the owner's `github_username` and the transcript's
 *  `project_hash` — mirrors `ownerUsername`/`projectHash` in
 *  `SessionDetailV2`, fed into `buildProjectHref` below so the harness
 *  captures a real breadcrumb href rather than a label-only crumb. */
const SAMPLE_OWNER_USERNAME = "demo-owner";
const SAMPLE_PROJECT_HASH = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2";

/**
 * Visual-regression harness host — a DEV-ONLY fixture mount of the SAME
 * fairtrade `<TranscriptViewer>` composite the production `/transcripts/[id]`
 * page renders, fed a bundled `SessionDetailPayload` fixture instead of a
 * REST fetch. This is the village-side capture host: it lets the
 * harness (`scripts/visual/`) drive every transcript surface from a plain
 * `next dev` with no backend/auth/seed data, and pair each shot against the
 * canonical fairtrade demo (which renders the same `sess_demo_0001`) for a true
 * height-matched, same-data side-by-side.
 *
 * It mounts the canonical viewer exactly as the real page does: detected phases,
 * derived annotations, and the @xyflow graph engine in Fairtrade's graph slot.
 * The host is height-bounded so the viewer's transcript stream owns scrolling and
 * can reveal its sticky scrubber. All capabilities are enabled with inert host
 * callbacks so every action affordance is available for capture. It 404s in a
 * production build, so it never ships as a public route.
 */
export default function VisualHarnessPage() {
  // Not a product surface — only reachable under `next dev`. In a production
  // build it 404s so it never ships as a public route.
  if (process.env.NODE_ENV === "production") notFound();

  // Theme is driven exactly like the real app: `[data-theme]` on the document
  // element (fairtrade's token selectors are attribute-based, so the composite
  // picks it up via the cascade). The harness owns it deterministically — dark by
  // default — and the `.theme-btn` flips it; the capture script asserts the
  // resulting `[data-theme]` value.
  const [theme, setTheme] = useState<Theme>("dark");
  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);

  const turns = useMemo(() => sampleSession.turns ?? [], []);
  const phases = useMemo(() => detectPhases(turns), [turns]);
  const annotations = useMemo(() => annotateTranscript(turns), [turns]);

  // The ONE wire→view projection, exactly as the real page cooks it — including
  // the stored-title overlay `SessionDetailV2` applies (village#32): the
  // composite's own fallback chain derives a heading from the first
  // `role: user` turn, which can surface raw harness preamble, so the hero
  // must always show the stored title (or the explore card's own empty-title
  // label) instead.
  const vm = useMemo(() => {
    const analytics = computeAnalytics(
      turns as Parameters<typeof computeAnalytics>[0],
      { scorecard: sampleSession.scorecard ?? undefined } as Parameters<typeof computeAnalytics>[1],
    );
    const adapted = adaptTranscript(
      sampleSession as Parameters<typeof adaptTranscript>[0],
      undefined,
      analytics,
    );
    return overlayStoredTitle(adapted, SAMPLE_TRANSCRIPT_TITLE);
  }, [turns]);
  const toolVMsByTurn = useMemo(
    () => new Map(vm.turns.map((turn) => [turn.index, turn.toolCalls])),
    [vm],
  );

  // Persisted-label echo: the popover hands us a `TurnLabel`; with no backend we
  // just hand it straight back so the chip renders.
  const handleLabelSave = async (label: TurnLabel): Promise<TurnLabel> => label;

  const project = sampleSession.project ?? "transcript";
  const projectHref = buildProjectHref(SAMPLE_OWNER_USERNAME, SAMPLE_PROJECT_HASH);

  return (
    <div
      data-theme={theme}
      style={{ background: "var(--canvas)", color: "var(--ink)", minHeight: "100vh" }}
    >
      {/* Harness chrome — identity + the theme toggle the capture script drives.
          A non-scrolling sticky strip so the `.theme-btn` is always reachable. */}
      <header
        className="vh-header"
        style={{
          position: "sticky",
          top: 0,
          zIndex: 60,
          display: "flex",
          alignItems: "center",
          gap: "1rem",
          padding: "0.5rem 1.25rem",
          minHeight: "44px",
          background: "var(--surface)",
          borderBottom: "1px solid var(--rule)",
          fontFamily: "var(--font-mono)",
        }}
      >
        <strong style={{ fontSize: "16px", letterSpacing: "-0.01em" }}>
          Transcript visual harness
        </strong>
        <span style={{ color: "var(--ink-3)", fontSize: "13px" }}>
          fixture · {sampleSession.id}
        </span>
        <button
          type="button"
          className="theme-btn"
          aria-label="toggle theme"
          title="toggle theme"
          onClick={() => setTheme((t) => (t === "dark" ? "light" : "dark"))}
          style={{
            marginLeft: "auto",
            display: "inline-flex",
            alignItems: "center",
            gap: "0.4rem",
            padding: "0.35rem 0.6rem",
            background: "var(--surface)",
            color: "var(--ink)",
            border: "1px solid var(--rule)",
            borderRadius: "6px",
            cursor: "pointer",
            font: "inherit",
          }}
        >
          {theme === "dark" ? <Moon size={16} aria-hidden /> : <Sun size={16} aria-hidden />}
          <span>{theme}</span>
        </button>
      </header>

      {/* Bounded host: Fairtrade's canonical stream scrolls internally. */}
      <div style={{ height: "calc(100vh - 45px)" }}>
        <TranscriptViewer
          viewModel={vm}
          theme={theme}
          // All capabilities on so every action affordance renders for capture;
          // the demo enables the same set.
          capabilities={{
            canLabel: true,
            canEdit: true,
            canChangeVisibility: true,
            canContribute: true,
            canExport: true,
          }}
          callbacks={{
            onEdit: () => {},
            onContribute: () => {},
            onChangeVisibility: () => {},
            onCopyLink: () => {},
          }}
          // Built through the SAME shared builder SessionDetailV2 calls
          // (village#32/#33), so this harness cannot drift onto a
          // hand-copied second rule.
          breadcrumb={buildTranscriptBreadcrumb({
            project,
            projectHref,
            storedTitle: SAMPLE_TRANSCRIPT_TITLE,
            transcriptId: SAMPLE_TRANSCRIPT_ID,
          })}
          graphSlot={() => (
            <TrajectoryGraph
              turns={turns}
              toolVMsByTurn={toolVMsByTurn}
              filteredTurns={turns}
              phases={phases}
              annotations={annotations}
              searchMatches={[]}
              provider={sampleSession.harness}
            />
          )}
          // Village's real per-turn label control (the same slot production
          // uses), so the harness captures the actual popover. Self-contained
          // (static annotation types, no fetch); the save is the no-op echo.
          renderTurnActions={(turn) => (
            <TurnLabelPopover entryIndex={turn.index} onSave={handleLabelSave} />
          )}
        />
      </div>
    </div>
  );
}
