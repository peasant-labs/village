"use client";

import { useMemo, useState } from "react";
import {
  ChevronRight,
  GitBranch,
  ExternalLink,
  FolderGit2,
  HelpCircle,
} from "lucide-react";
import type { GroupTranscript } from "@/lib/types";
import { childSessionsByParentID, groupChildSessions } from "@/lib/childSessions";
import { collectiveTranscriptRow, formatCompact, groupByRepo } from "@/lib/format";
import TranscriptList, {
  type TranscriptRowFact,
} from "@/components/transcript/TranscriptList";

interface CollectiveReposProps {
  /** Transcripts visible to the current viewer (recent page or full browse). */
  transcripts: GroupTranscript[];
  /** Whether the viewer is a collective owner (sees real handles regardless of
   *  discoverability). Who the viewer IS is not passed: the rows are drawn by
   *  the shared transcript list, which reads the signed-in person itself, and
   *  two answers to "who is looking" could disagree. */
  viewerIsOwner?: boolean;
}

/**
 * What a row under a repository states: which model ran, on which branch, and
 * when. The repository above it already names the project and the remote, so a
 * row does not repeat them.
 */
const REPO_TRANSCRIPT_FACTS: readonly TranscriptRowFact[] = ["model", "branch", "date"];

/** Normalise a git remote into an https GitHub-style URL when possible. */
function remoteHref(remote: string | null): string | null {
  if (!remote) return null;
  let r = remote.trim().replace(/\.git$/, "");
  // scp-style: git@github.com:owner/repo
  const scp = r.match(/^git@([^:]+):(.+)$/);
  if (scp) r = `https://${scp[1]}/${scp[2]}`;
  else if (r.startsWith("ssh://")) r = "https://" + r.slice("ssh://".length).replace(/^git@/, "");
  else if (!/^https?:\/\//.test(r)) {
    // bare host/owner/repo form (e.g. "github.com/user/repo")
    if (/^[\w.-]+\/[\w./-]+$/.test(r)) r = `https://${r}`;
    else return null;
  }
  return r;
}

/**
 * Groups a collective's transcripts by repository — the village analog of how
 * the local peasant app relates transcripts to repos. The repo key is derived
 * from each transcript's git remote (in its gitContext), falling back to an
 * "Unattributed" bucket when no remote is present.
 *
 * Pure client-side: the data already arrives on each `GroupTranscript`
 * (`git_remote`, `project_name`, token counts, owner). No extra fetch.
 */
export default function CollectiveRepos({
  transcripts,
  viewerIsOwner = false,
}: CollectiveReposProps) {
  const repoGroups = useMemo(() => groupByRepo(transcripts), [transcripts]);
  // One fold per repository, computed once rather than at each render of an
  // expanded panel, and keyed by the repository the rows belong to.
  const foldsByRepo = useMemo(
    () =>
      new Map(
        repoGroups.map((repo) => [
          repo.key,
          groupChildSessions(repo.transcripts.map(collectiveTranscriptRow)),
        ]),
      ),
    [repoGroups],
  );
  const [expanded, setExpanded] = useState<Set<string>>(
    // Expand the largest repo by default so the section isn't an empty wall.
    () => new Set(repoGroups.length > 0 ? [repoGroups[0].key] : [])
  );

  function toggle(key: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  if (repoGroups.length === 0) return null;

  return (
    <div className="border border-rule bg-surface">
      <div className="flex items-center justify-between px-5 py-3 border-b border-rule">
        <div className="flex items-center gap-2">
          <FolderGit2 className="size-3.5 text-ink-3" />
          <span className="text-sm font-medium text-ink">Repositories</span>
        </div>
        <span className="text-xs font-mono text-ink-3 tabular-nums">
          {repoGroups.length}
        </span>
      </div>

      <div className="divide-y divide-rule">
        {repoGroups.map((repo) => {
          const isExpanded = expanded.has(repo.key);
          const href = remoteHref(repo.remote);
          const fold = foldsByRepo.get(repo.key)!;
          return (
            <div key={repo.key}>
              {/* Repo header row */}
              <button
                type="button"
                onClick={() => toggle(repo.key)}
                aria-expanded={isExpanded}
                className="w-full flex items-center gap-3 px-5 py-3 text-left hover:bg-surface-hover transition-colors cursor-pointer focus-mono"
              >
                <ChevronRight
                  className={`size-3.5 shrink-0 text-ink-3 transition-transform duration-150 ${
                    isExpanded ? "rotate-90" : ""
                  }`}
                />
                {repo.unattributed ? (
                  <HelpCircle className="size-4 shrink-0 text-ink-4" />
                ) : (
                  <GitBranch className="size-4 shrink-0 text-ink-3" />
                )}
                <div className="min-w-0 flex-1 flex flex-col">
                  <span className="font-[family-name:var(--font-display)] text-sm text-ink tracking-tight truncate">
                    {repo.name}
                  </span>
                  {repo.remote ? (
                    <span
                      className="text-[11px] font-mono text-ink-3 truncate"
                      title={repo.remote}
                    >
                      {repo.remote}
                    </span>
                  ) : (
                    <span className="text-[11px] font-mono text-ink-4">
                      No git remote
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-4 shrink-0 text-[11px] font-mono text-ink-3 tabular-nums">
                  <span title="Transcripts">
                    {repo.transcriptCount} transcript
                    {repo.transcriptCount !== 1 ? "s" : ""}
                  </span>
                  <span title="Contributors" className="hidden sm:inline">
                    {repo.contributorCount} contributor
                    {repo.contributorCount !== 1 ? "s" : ""}
                  </span>
                  <span title="Total tokens" className="hidden sm:inline">
                    {formatCompact(repo.totalTokens)} tok
                  </span>
                </div>
                {href && (
                  <a
                    href={href}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    title="Open repository"
                    className="shrink-0 inline-flex size-6 items-center justify-center text-ink-4 hover:text-ink hover:bg-surface-hover transition-colors cursor-pointer focus-mono"
                  >
                    <ExternalLink className="size-3.5" />
                  </a>
                )}
              </button>

              {/* Transcript list for this repo, drawn by the SAME list every
                  other transcript surface uses. Folded WITHIN the repository,
                  which is where a starter and what it started both sit: a
                  session and the sessions it starts share a git remote, so
                  grouping the repository's own rows is what puts a started
                  session under the row a reader is already looking at. */}
              {isExpanded && (
                <div className="border-t border-rule bg-canvas/40 pl-7">
                  <TranscriptList
                    items={fold.rootItems}
                    childSessions={childSessionsByParentID(fold)}
                    facts={REPO_TRANSCRIPT_FACTS}
                    viewerIsPrivileged={viewerIsOwner}
                    bare
                  />
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
