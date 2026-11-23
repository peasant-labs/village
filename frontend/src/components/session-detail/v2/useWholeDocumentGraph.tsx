'use client';

import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import type { SessionRelationshipNavigation } from '@peasant-labs/schema';
import RequestFailureState from '@/components/RequestFailureState';
import { readWholeTranscript, WholeTranscriptPurpose, type WholeTranscript } from '@/lib/transcriptExport';
import { SessionDetailV2TestId } from './readModeTestIds';
import { TranscriptViewMode } from './transcriptViewMode';
import { WholeDocumentGraph } from './WholeDocumentGraph';

/** The page's current read epoch: whole-document reads carry its signal, and a read whose epoch
 *  has ended is discarded. The page ends it on a revision change or an unmount. */
export interface WholeDocumentReadEpoch {
  id: number;
  abort: AbortController;
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

export interface WholeDocumentGraphOptions {
  /** Whether the page reads positioned; a whole-document page draws its graph from what it read. */
  positioned: boolean;
  viewMode: TranscriptViewMode;
  /** The revision the positioned reader shows; an answer for another revision is dropped. */
  revision: string | undefined;
  transcriptId: string;
  storedTitle: string | null | undefined;
  knownHarness?: string;
  relationshipNavigation?: SessionRelationshipNavigation[];
  /** The page's read epoch, shared with its downloads. */
  epoch: RefObject<WholeDocumentReadEpoch>;
}

/**
 * The positioned page's trajectory graph, drawn from the whole stored copy read once when the
 * reader opens the graph: a reader action like a download, never an answer to a positioned
 * failure. The list never reads it. The read carries the page's epoch, so a revision change or an
 * unmount aborts it and discards its answer. Returns the graph slot's render function.
 */
export function useWholeDocumentGraph({
  positioned,
  viewMode,
  revision,
  transcriptId,
  storedTitle,
  knownHarness,
  relationshipNavigation,
  epoch: epochRef,
}: WholeDocumentGraphOptions): () => ReactNode {
  const [graphRead, setGraphRead] = useState<GraphRead>({ status: GraphReadStatus.Idle });
  const graphCurrent: GraphRead =
    graphRead.status !== GraphReadStatus.Idle && graphRead.revision === revision ? graphRead : { status: GraphReadStatus.Idle };
  const graphWanted = positioned && viewMode === TranscriptViewMode.Graph;
  // The revision a read is in flight for; a second render never starts a second read.
  const graphRequested = useRef<{ revision: string | undefined } | null>(null);
  useEffect(() => {
    if (!graphWanted || graphCurrent.status !== GraphReadStatus.Idle) return;
    if (graphRequested.current && graphRequested.current.revision === revision) return;
    graphRequested.current = { revision };
    const { id, abort } = epochRef.current;
    const epoch = { signal: abort.signal, isCurrent: () => epochRef.current.id === id };
    readWholeTranscript({ transcriptId, storedTitle, knownHarness, relationshipNavigation }, epoch, WholeTranscriptPurpose.Graph).then(
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
  }, [graphWanted, graphCurrent.status, revision, transcriptId, storedTitle, knownHarness, relationshipNavigation, epochRef]);

  const retry = useCallback(() => {
    graphRequested.current = null;
    setGraphRead({ status: GraphReadStatus.Idle });
  }, []);
  return function renderPositionedGraph() {
    return <PositionedGraph read={graphCurrent} onRetry={retry} />;
  };
}
