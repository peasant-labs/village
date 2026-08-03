"use client";

import { useState } from "react";
import {
  FolderGit2,
  GitCommitHorizontal,
  Github,
  Loader2,
  Lock,
  PlugZap,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { Button, Input, Tag } from "@/lib/ft-ui";
import {
  useRepositories,
  useLinkRepository,
  useUnlinkRepository,
  useRepositoryCommits,
  isNotConfigured,
} from "@/lib/queries/repositories";
import type { GroupTranscript, LinkedRepository } from "@/lib/types";
import CommitTimeline from "./CommitTimeline";

interface LinkedRepositoriesProps {
  groupId: string;
  /** Whether the current viewer is a collective owner (`role === "owner"`). */
  isOwner: boolean;
  /**
   * The collective's transcripts the viewer can see (the same list the
   * repo-grouping + analytics consume). Threaded down to the commit-timeline
   * overlay, which joins each transcript's commit SHAs against a repo's cached
   * commits. Defaults to empty — the overlay degrades to a plain commit list.
   */
  transcripts?: GroupTranscript[];
}

/** Parse a free-text `owner/name` (or `owner / name`) field into parts. */
function parseRepoInput(raw: string): { owner: string; name: string } | null {
  const trimmed = raw.trim().replace(/^https?:\/\/github\.com\//i, "");
  const parts = trimmed
    .split("/")
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length !== 2) return null;
  return { owner: parts[0], name: parts[1].replace(/\.git$/i, "") };
}

function formatDate(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

// ---------------------------------------------------------------------------
// Not-configured notice — the GitHub App is config-gated, and every endpoint
// returns 501 when it is absent. We render this inline, calm and informative,
// instead of an alarming error toast.
// ---------------------------------------------------------------------------

function NotConfiguredNotice() {
  return (
    <div className="flex items-start gap-3 border border-rule bg-surface-hover px-4 py-3">
      <PlugZap className="size-4 shrink-0 text-ink-3 mt-0.5" />
      <div className="flex flex-col gap-0.5">
        <p className="text-[13px] font-medium text-ink">
          GitHub connection isn&apos;t set up
        </p>
        <p className="text-[12px] text-ink-3">
          An admin must register the GitHub App on this server before
          repositories can be linked. Nothing is broken — this feature is just
          turned off here.
        </p>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Per-repo cached commits — fetched lazily when a row is expanded.
// ---------------------------------------------------------------------------

function RepositoryCommits({
  groupId,
  repo,
  isOwner,
  transcripts,
}: {
  groupId: string;
  repo: LinkedRepository;
  isOwner: boolean;
  transcripts: GroupTranscript[];
}) {
  // `refresh` re-fetches from GitHub (owner-only on the backend). We flip it on
  // and let React Query treat the refreshed read as a distinct fetch.
  const [refresh, setRefresh] = useState(false);
  const commits = useRepositoryCommits(groupId, repo.owner, repo.name, {
    refresh,
    enabled: true,
  });

  if (commits.isLoading) {
    return (
      <div className="flex items-center gap-2 px-4 py-3 text-[12px] text-ink-3">
        <Loader2 className="size-3.5 animate-spin" />
        Loading commits…
      </div>
    );
  }

  if (commits.isError) {
    if (isNotConfigured(commits.error)) return <NotConfiguredNotice />;
    return (
      <div className="px-4 py-3 text-[12px] text-danger">
        Couldn&apos;t load commits: {commits.error.message}
      </div>
    );
  }

  const rows = commits.data?.commits ?? [];

  return (
    <div className="flex flex-col gap-2 px-4 py-3 bg-canvas/40">
      <div className="flex items-center justify-between">
        <span className="v2-eyebrow">
          Cached commits
          {commits.data ? ` (${commits.data.commit_count})` : ""}
        </span>
        {isOwner && (
          <Button
            size="sm"
            variant="secondary"
            icon={RefreshCw}
            loading={commits.isFetching}
            disabled={commits.isFetching}
            onClick={() => {
              // First click enables refresh; subsequent clicks refetch the
              // refresh query. Owners only — the backend gates refresh to owners.
              if (!refresh) setRefresh(true);
              else commits.refetch();
            }}
          >
            Refresh
          </Button>
        )}
      </div>

      {rows.length === 0 ? (
        <p className="text-[12px] text-ink-3 py-2">
          No commits cached yet.
          {isOwner
            ? " Use Refresh to pull them from GitHub."
            : " An owner can refresh to pull them from GitHub."}
        </p>
      ) : (
        // The commit-timeline overlay renders the commit list itself and joins
        // the collective's transcripts onto it by SHA (see CommitTimeline). It
        // also handles the "no transcripts" / "no matches" sub-states inline.
        <CommitTimeline repoCommits={rows} transcripts={transcripts} />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Owner-only link form. Backend requires owner, name, AND a GitHub App
// installation_id (the install that grants the App access to the repo).
// ---------------------------------------------------------------------------

function LinkRepoForm({ groupId }: { groupId: string }) {
  const [repoInput, setRepoInput] = useState("");
  const [installationId, setInstallationId] = useState("");
  const [validationError, setValidationError] = useState<string | null>(null);
  const link = useLinkRepository();

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setValidationError(null);

    const parsed = parseRepoInput(repoInput);
    if (!parsed) {
      setValidationError("Enter a repository as owner/name (e.g. acme/widgets).");
      return;
    }
    const instId = Number(installationId.trim());
    if (!Number.isInteger(instId) || instId <= 0) {
      setValidationError("Enter the numeric GitHub App installation ID.");
      return;
    }

    link.mutate(
      { groupId, owner: parsed.owner, name: parsed.name, installationId: instId },
      {
        onSuccess: () => {
          setRepoInput("");
          setInstallationId("");
        },
      }
    );
  }

  return (
    <form
      onSubmit={handleSubmit}
      className="flex flex-col gap-2 border-b border-rule px-4 py-3"
    >
      <span className="v2-eyebrow">Link a repository</span>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
        <div className="flex-1 [&_.is-field]:mb-0">
          <Input
            value={repoInput}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => setRepoInput(e.target.value)}
            placeholder="owner/name"
            aria-label="Repository owner/name"
          />
        </div>
        <div className="sm:w-44 [&_.is-field]:mb-0">
          <Input
            value={installationId}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
              setInstallationId(e.target.value.replace(/[^\d]/g, ""))
            }
            inputMode="numeric"
            placeholder="installation id"
            aria-label="GitHub App installation ID"
          />
        </div>
        <Button
          type="submit"
          size="sm"
          variant="primary"
          icon={Github}
          loading={link.isPending}
          disabled={link.isPending}
        >
          Link
        </Button>
      </div>
      <p className="text-[11px] text-ink-3">
        The GitHub App must already be installed on the repo. The installation
        ID comes from that installation.
      </p>
      {validationError && (
        <p className="text-[12px] text-danger">{validationError}</p>
      )}
      {link.isError && !validationError && (
        <p className="text-[12px] text-danger">
          {isNotConfigured(link.error)
            ? "GitHub connection isn't set up on this server."
            : link.error.message}
        </p>
      )}
    </form>
  );
}

// ---------------------------------------------------------------------------
// Section: linked repositories. Visible to all members (read-only for
// non-owners); owners get the link form, unlink action, and commit refresh.
// Owner-only controls are gated on `isOwner` (role === "owner"), the same gate
// the rest of the collective UI and the backend use.
// ---------------------------------------------------------------------------

export default function LinkedRepositories({
  groupId,
  isOwner,
  transcripts = [],
}: LinkedRepositoriesProps) {
  const repos = useRepositories(groupId);
  const unlink = useUnlinkRepository();
  const [expanded, setExpanded] = useState<string | null>(null);
  const [confirmingUnlink, setConfirmingUnlink] = useState<string | null>(null);

  function repoKey(r: LinkedRepository): string {
    return `${r.owner}/${r.name}`;
  }

  // 501 from the list endpoint == the GitHub App is not configured. Show the
  // calm inline notice in place of the list (and hide the owner link form,
  // since linking can't work either).
  const notConfigured = repos.isError && isNotConfigured(repos.error);

  return (
    <div className="border border-rule bg-surface">
      <div className="flex items-center justify-between px-5 py-3 border-b border-rule">
        <div className="flex items-center gap-2">
          <FolderGit2 className="size-3.5 text-ink-3" />
          <span className="text-sm font-medium text-ink">
            linked repositories
          </span>
        </div>
        {repos.data && (
          <span className="text-xs font-mono text-ink-3 tabular-nums">
            {repos.data.repositories.length}
          </span>
        )}
      </div>

      <div>
        {notConfigured ? (
          <div className="px-4 py-4">
            <NotConfiguredNotice />
          </div>
        ) : (
          <>
            {isOwner && <LinkRepoForm groupId={groupId} />}

            {repos.isLoading ? (
              <div className="flex items-center gap-2 px-5 py-6 text-[13px] text-ink-3">
                <Loader2 className="size-4 animate-spin" />
                Loading repositories…
              </div>
            ) : repos.isError ? (
              <div className="px-5 py-6 text-[13px] text-danger">
                {repos.error.message}
              </div>
            ) : (repos.data?.repositories.length ?? 0) === 0 ? (
              <div className="px-5 py-8 text-center">
                <FolderGit2 className="size-6 text-ink-4 mx-auto mb-2" />
                <p className="text-[13px] text-ink-3">
                  No repositories linked yet.
                  {isOwner
                    ? " Link one above to cache its commit history."
                    : " An owner can link repositories to this collective."}
                </p>
              </div>
            ) : (
              <div className="divide-y divide-rule">
                {repos.data!.repositories.map((repo) => {
                  const key = repoKey(repo);
                  const isExpanded = expanded === key;
                  const isConfirming = confirmingUnlink === key;
                  return (
                    <div key={repo.id || key}>
                      <div className="flex items-center gap-3 px-5 py-3">
                        <Github className="size-4 shrink-0 text-ink-3" />
                        <div className="min-w-0 flex-1 flex flex-col">
                          <span className="text-[13px] text-ink truncate">
                            {repo.owner}/{repo.name}
                          </span>
                          <span className="text-[11px] font-mono text-ink-4 tabular-nums">
                            synced {formatDate(repo.last_synced_at)}
                          </span>
                        </div>
                        {repo.is_private && (
                          <Tag icon={Lock} className="shrink-0">
                            Private
                          </Tag>
                        )}
                        <Button
                          size="sm"
                          variant="secondary"
                          icon={GitCommitHorizontal}
                          className="shrink-0"
                          onClick={() =>
                            setExpanded(isExpanded ? null : key)
                          }
                        >
                          {isExpanded ? "Hide" : "Commits"}
                        </Button>

                        {isOwner &&
                          (isConfirming ? (
                            <div className="flex items-center gap-1.5 shrink-0">
                              <span className="text-[11px] font-mono text-ink-3">
                                Unlink?
                              </span>
                              <Button
                                size="sm"
                                variant="danger"
                                loading={unlink.isPending}
                                disabled={unlink.isPending}
                                onClick={() =>
                                  unlink.mutate(
                                    {
                                      groupId,
                                      owner: repo.owner,
                                      name: repo.name,
                                    },
                                    {
                                      onSuccess: () =>
                                        setConfirmingUnlink(null),
                                    }
                                  )
                                }
                              >
                                Yes
                              </Button>
                              <Button
                                size="sm"
                                variant="secondary"
                                onClick={() => setConfirmingUnlink(null)}
                              >
                                Cancel
                              </Button>
                            </div>
                          ) : (
                            <Button
                              size="sm"
                              variant="secondary"
                              icon={Trash2}
                              aria-label={`Unlink ${key}`}
                              title="Unlink repository"
                              className="shrink-0"
                              onClick={() => setConfirmingUnlink(key)}
                            />
                          ))}
                      </div>

                      {isExpanded && (
                        <div className="border-t border-rule">
                          <RepositoryCommits
                            groupId={groupId}
                            repo={repo}
                            isOwner={isOwner}
                            transcripts={transcripts}
                          />
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
