"use client";

import { useMemo, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { GitCommitHorizontal, Loader2 } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";
import { DataTable, Tag } from "@/lib/ft-ui";
import { api, isApiErrorStatus } from "@/lib/api";
import { isNotConfigured } from "@/lib/queries/repositories";
import type {
  GroupTranscript,
  RepositoryCommit,
  TranscriptCommitsResponse,
} from "@/lib/types";

interface CommitTimelineProps {
  /** The linked repo's cached commits (newest→oldest as returned by the API). */
  repoCommits: RepositoryCommit[];
  /**
   * The collective's transcripts the viewer can see — the same list the
   * repo-grouping and analytics use. Each transcript's recorded commit SHAs are
   * joined against `repoCommits` to overlay which transcripts touched a commit.
   */
  transcripts: GroupTranscript[];
}

/** A repo commit enriched with the transcripts whose commits share its SHA. */
interface OverlayedCommit {
  commit: RepositoryCommit;
  transcripts: GroupTranscript[];
}

function formatDate(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

// fairtrade does not re-export its DataTable column type from the barrel; mirror
// the shape locally for the overlay's column defs.
type DataTableColumn = {
  key: string;
  label: ReactNode;
  sortable?: boolean;
  align?: "left" | "right" | "center";
  width?: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  render?: (value: any, row: any) => ReactNode;
};

// DataTable column model for the commit overlay. A commit "touched" by ≥1
// transcript is marked by a leading commit glyph in the SHA cell (icon + shape,
// never color alone), and its matching transcripts render as fairtrade Tags.
const OVERLAY_COLUMNS: DataTableColumn[] = [
  {
    key: "sha",
    label: "SHA",
    width: "7rem",
    render: (sha, row) => (
      <span className="inline-flex items-center gap-1.5 font-mono text-[11px] text-ink-3 tabular-nums">
        {row.matched && (
          <GitCommitHorizontal className="size-3 text-ink shrink-0" />
        )}
        {String(sha).slice(0, 7)}
      </span>
    ),
  },
  {
    key: "message",
    label: "Message",
    render: (message) => (
      <span className="block max-w-0 truncate text-[12px] text-ink">
        {(message ?? "").split("\n")[0] || "—"}
      </span>
    ),
  },
  {
    key: "transcripts",
    label: "Transcripts",
    render: (matched: GroupTranscript[]) =>
      matched.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {matched.map((t) => (
            <Link
              key={t.id}
              href={`/transcripts/${t.id}`}
              title={t.title || "Untitled"}
              className="focus-mono cursor-pointer"
            >
              <Tag className="max-w-[160px] truncate">
                {t.title || "Untitled"}
              </Tag>
            </Link>
          ))}
        </div>
      ) : (
        <span className="text-[11px] font-mono text-ink-4">—</span>
      ),
  },
  {
    key: "date",
    label: "Date",
    align: "right",
    sortable: true,
    width: "7rem",
    render: (iso) => (
      <span className="whitespace-nowrap font-mono text-[11px] text-ink-3 tabular-nums">
        {formatDate(iso)}
      </span>
    ),
  },
];

/**
 * The commit-timeline overlay. It joins the collective's transcripts onto a
 * linked repo's commit timeline by commit SHA:
 *
 *  1. Fan out a `GET /transcripts/{id}/commits` read per transcript (React Query
 *     `useQueries`, per-id — fine for v1; each read is cached aggressively).
 *  2. Index those transcript commits by SHA into a `sha → transcript[]` map.
 *  3. Walk the repo's commits (newest→oldest) and attach the matching
 *     transcripts. Commits with ≥1 transcript stand out and can be filtered to.
 *
 * The map keys on the full SHA; transcript and repo commit SHAs come from the
 * same git history, so an exact match is the correct join (no prefix matching).
 */
export default function CommitTimeline({
  repoCommits,
  transcripts,
}: CommitTimelineProps) {
  const [onlyMatched, setOnlyMatched] = useState(false);

  // Per-transcript commit reads. `useQueries` keeps hook order stable across
  // renders even as the transcript list changes, and dedupes/caches via the
  // shared ["transcript-commits", id] key (same as the useTranscriptCommits
  // hook). A 403/501 on any single transcript is terminal for that read.
  const commitQueries = useQueries({
    queries: transcripts.map((t) => ({
      queryKey: ["transcript-commits", t.id],
      queryFn: () =>
        api<TranscriptCommitsResponse>(
          `/transcripts/${encodeURIComponent(t.id)}/commits`
        ),
      staleTime: 5 * 60 * 1000,
      retry: (failureCount: number, err: unknown) => {
        if (isNotConfigured(err) || isApiErrorStatus(err, 403)) return false;
        return failureCount < 2;
      },
    })),
  });

  const anyLoading = commitQueries.some((q) => q.isLoading);
  const loadedCount = commitQueries.filter((q) => q.isSuccess).length;

  // sha → transcripts[] map, built from the per-transcript commit reads.
  const shaToTranscripts = (() => {
    const map = new Map<string, GroupTranscript[]>();
    commitQueries.forEach((q, i) => {
      const transcript = transcripts[i];
      if (!transcript || !q.data) return;
      for (const c of q.data.commits) {
        const existing = map.get(c.sha);
        if (existing) {
          // Dedup: a transcript can record the same SHA more than once.
          if (!existing.some((t) => t.id === transcript.id)) {
            existing.push(transcript);
          }
        } else {
          map.set(c.sha, [transcript]);
        }
      }
    });
    return map;
  })();

  const overlayed: OverlayedCommit[] = useMemo(
    () =>
      repoCommits.map((commit) => ({
        commit,
        transcripts: shaToTranscripts.get(commit.sha) ?? [],
      })),
    [repoCommits, shaToTranscripts]
  );

  const matchedCount = overlayed.filter((o) => o.transcripts.length > 0).length;
  const visible = onlyMatched
    ? overlayed.filter((o) => o.transcripts.length > 0)
    : overlayed;

  const tableRows = visible.map(({ commit, transcripts: matched }) => ({
    id: commit.sha,
    sha: commit.sha,
    message: commit.message,
    transcripts: matched,
    date: commit.committed_at ?? commit.authored_at,
    matched: matched.length > 0,
  }));

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <span className="v2-eyebrow">
          Transcript overlay
          {transcripts.length > 0 &&
            ` · ${matchedCount} of ${repoCommits.length} commit${
              repoCommits.length === 1 ? "" : "s"
            } touched`}
        </span>
        <div className="flex items-center gap-2">
          {anyLoading && (
            <span className="inline-flex items-center gap-1.5 text-[11px] font-mono text-ink-4 tabular-nums">
              <Loader2 className="size-3 animate-spin" />
              {loadedCount}/{transcripts.length}
            </span>
          )}
          {/* Filter: all commits vs only those with ≥1 transcript. */}
          <div className="inline-flex border border-rule overflow-hidden">
            <button
              type="button"
              onClick={() => setOnlyMatched(false)}
              aria-pressed={!onlyMatched}
              className={`px-2.5 py-1 text-[11px] font-mono transition-colors cursor-pointer focus-mono ${
                !onlyMatched
                  ? "bg-ink text-canvas"
                  : "text-ink-3 hover:text-ink hover:bg-surface-hover"
              }`}
            >
              All
            </button>
            <button
              type="button"
              onClick={() => setOnlyMatched(true)}
              aria-pressed={onlyMatched}
              disabled={matchedCount === 0}
              className={`px-2.5 py-1 text-[11px] font-mono border-l border-rule transition-colors cursor-pointer focus-mono disabled:opacity-50 disabled:cursor-not-allowed ${
                onlyMatched
                  ? "bg-ink text-canvas"
                  : "text-ink-3 hover:text-ink hover:bg-surface-hover"
              }`}
            >
              With transcripts
            </button>
          </div>
        </div>
      </div>

      {repoCommits.length === 0 ? (
        <p className="text-[12px] text-ink-3 py-2">
          No commits to overlay yet.
        </p>
      ) : transcripts.length === 0 ? (
        <p className="text-[12px] text-ink-3 py-2">
          No collective transcripts to match against these commits.
        </p>
      ) : visible.length === 0 ? (
        <p className="text-[12px] text-ink-3 py-2">
          None of this collective&apos;s transcripts touch these commits yet.
        </p>
      ) : (
        <div className="border border-rule overflow-x-auto [&_.tbl-wrap]:mt-0">
          <DataTable
            columns={OVERLAY_COLUMNS}
            rows={tableRows}
            rowKey={(r: { sha: string }) => r.sha}
          />
        </div>
      )}
    </div>
  );
}
