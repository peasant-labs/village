"use client";

import { useEffect, useMemo, useState } from "react";
import { notFound } from "next/navigation";
import {
  TrajectoryGraph,
  annotateTranscript,
  type TurnLabel,
} from "@peasant-labs/transcript-browser";
// The harness mounts what production mounts: fairtrade's TranscriptViewer
// composite (the same surface the demo renders), not tb's retired composer.
import {
  TranscriptViewer,
  adaptTranscript,
  computeAnalytics,
} from "@peasant-labs/fairtrade/ui";
import "@xyflow/react/dist/style.css";
import { Moon, Sun } from "lucide-react";
import { detectPhases } from "@/lib/insights";
import { displayProject } from "@/lib/quality/utils";
import TurnLabelPopover from "@/components/transcript/TurnLabelPopover";
import { sampleSession } from "./sample-session";

type Theme = "dark" | "light";

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
 * It mounts the composer exactly as the real page does (same `<SessionDetail>`
 * props: detected phases, derived annotations, the @xyflow graph in the graph
 * slot) but with all capabilities on and host callbacks stubbed, so every action
 * affordance renders for capture. The composer manages its OWN page scroll + a
 * sticky condensed header (it is not a height-bounded inner-scroller), so the
 * host here is plain document flow — the capture script grows the viewport to the
 * full document height for full-surface shots and scrolls the window to reveal
 * the sticky header. It 404s in a production build, so it never ships as a public
 * route.
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

  // The ONE wire→view projection, exactly as the real page cooks it.
  const vm = useMemo(() => {
    const analytics = computeAnalytics(
      turns as Parameters<typeof computeAnalytics>[0],
      { scorecard: sampleSession.scorecard ?? undefined } as Parameters<typeof computeAnalytics>[1],
    );
    return adaptTranscript(
      sampleSession as Parameters<typeof adaptTranscript>[0],
      undefined,
      analytics,
    );
  }, [turns]);
  const toolVMsByTurn = useMemo(
    () => new Map(vm.turns.map((turn) => [turn.index, turn.toolCalls])),
    [vm],
  );

  // Persisted-label echo: the popover hands us a `TurnLabel`; with no backend we
  // just hand it straight back so the chip renders.
  const handleLabelSave = async (label: TurnLabel): Promise<TurnLabel> => label;

  const project = sampleSession.project ?? "transcript";

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

      {/* Bounded host: the composite scrolls internally (sticky scrubber). */}
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
          breadcrumb={[
            { label: "dashboard", href: "/" },
            { label: "projects", href: "/projects" },
            {
              label: displayProject(project),
              href: `/projects/${encodeURIComponent(project)}`,
            },
            { label: sampleSession.id.slice(0, 8) },
          ]}
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
