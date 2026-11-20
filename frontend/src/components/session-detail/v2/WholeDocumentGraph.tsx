'use client';

import { useMemo } from 'react';
import { TrajectoryGraph } from '@peasant-labs/fairtrade/graph';
import { annotateTranscript } from '@peasant-labs/fairtrade/ui';
import type { SessionDetailPayload } from '@/types/messages';
import type { WholeTranscript } from '@/lib/transcriptExport';
import { detectPhases } from '@/lib/insights';

/**
 * The trajectory graph over a whole stored document: fairtrade's own `/graph`
 * engine, fed the host-derived inputs it needs (the composite derives its own).
 * The whole-document page draws it from the content it already read; the
 * positioned page draws it from the one read it makes when the graph opens.
 */
export function WholeDocumentGraph({ detail, viewModel }: { detail: SessionDetailPayload; viewModel: WholeTranscript['viewModel'] }) {
  const turns = useMemo(() => detail.turns ?? [], [detail]);
  const phases = useMemo(() => detectPhases(turns), [turns]);
  const annotations = useMemo(() => annotateTranscript(turns), [turns]);
  // Cooked tool calls by turn index, fed into the graph engine's tool nodes.
  const toolVMsByTurn = useMemo(
    () => new Map(viewModel.turns.map((turn) => [turn.index, turn.toolCalls])),
    [viewModel],
  );
  return (
    <TrajectoryGraph
      turns={turns}
      toolVMsByTurn={toolVMsByTurn}
      filteredTurns={turns}
      phases={phases}
      annotations={annotations}
      searchMatches={[]}
      provider={detail.harness}
    />
  );
}
