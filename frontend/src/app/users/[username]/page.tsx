"use client";

import { use, useEffect, useRef, useState } from "react";
import { useTranscripts, useRenameUserProject } from "@/lib/queries/transcripts";
import { useDeleteAccount, usePublicProfile, useUpdateMySettings } from "@/lib/queries/auth";
import { useAuth } from "@/providers/AuthProvider";
import TranscriptList from "@/components/transcript/TranscriptList";
import {
  Button,
  DataState,
  RailSection,
  RailShell,
  StatGrid,
  TeachingEmptyState,
} from "@/lib/ft-ui";
import PublishImportDialog from "@/app/publish/PublishImportDialog";
import { groupByProject } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  AlertTriangle,
  ChevronRight,
  EyeOff,
  FileText,
  FolderOpen,
  Library,
  Pencil,
  Plus,
  Trash2,
  Upload,
} from "lucide-react";
import Link from "next/link";

export default function UserProfilePage({
  params,
}: {
  params: Promise<{ username: string }>;
}) {
  const { username } = use(params);
  const { user } = useAuth();
  const { data: profile, isError: isProfileError } = usePublicProfile(username);
  const { data, isLoading } = useTranscripts({ owner: username });
  const renameProjectMutation = useRenameUserProject();
  const deleteAccountMutation = useDeleteAccount();
  const updateSettingsMutation = useUpdateMySettings();
  const [importOpen, setImportOpen] = useState(false);
  const [editingProject, setEditingProject] = useState<string | null>(null);
  const [draftName, setDraftName] = useState("");
  const editInputRef = useRef<HTMLInputElement | null>(null);

  const isOwnProfile =
    !!user && user.github_username.toLowerCase() === username.toLowerCase();
  const groups = data?.transcripts ? groupByProject(data.transcripts) : [];

  // Fall back to the session user for one's own profile so the owner is never
  // locked out of their settings even if the public profile fetch fails.
  const effectiveProfile = profile ?? (isOwnProfile ? user : undefined);
  const displayName = effectiveProfile?.display_name ?? username;
  const avatarUrl = effectiveProfile?.avatar_url ?? undefined;

  useEffect(() => {
    if (editingProject && editInputRef.current) {
      editInputRef.current.focus();
      editInputRef.current.select();
    }
  }, [editingProject]);

  function startEdit(project: string) {
    setEditingProject(project);
    setDraftName(project);
  }

  function cancelEdit() {
    setEditingProject(null);
    setDraftName("");
  }

  function commitEdit() {
    if (!editingProject) return;
    const next = draftName.trim();
    if (!next || next === editingProject) {
      cancelEdit();
      return;
    }
    renameProjectMutation.mutate(
      { from: editingProject, to: next },
      { onSettled: () => cancelEdit() }
    );
  }

  function deleteAccount() {
    if (
      confirm(
        "Permanently delete your account? This also removes your published transcripts and the collectives you own. This cannot be undone."
      )
    ) {
      deleteAccountMutation.mutate();
    }
  }

  if (isProfileError && !isOwnProfile) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <nav className="flex items-center gap-1.5 text-xs">
          <Link
            href="/"
            className="text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            Commons
          </Link>
          <ChevronRight size={12} strokeWidth={2} className="text-ink-4" />
          <span className="text-ink font-medium">@{username}</span>
        </nav>
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <EyeOff size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">User not found</p>
          <Link
            href="/"
            className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            Back to Commons
          </Link>
        </div>
      </div>
    );
  }

  // ── Library empty state ──────────────────────────────────────────────────────
  // TeachingEmptyState embedded inside the bordered library card. The component's
  // own cx-teach border + background are cleared so the card's hairline frames
  // the content uniformly across loading / empty / populated states.
  const libraryEmptyState = isOwnProfile ? (
    <div>
      <TeachingEmptyState
        icon={FolderOpen}
        title="your library is empty"
        body="publish a redacted transcript to share your work with the commons."
        privacy={null}
        style={{ border: "none", background: "transparent" }}
      />
      <div className="px-6 pb-6">
        <Link
          href="/publish"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-ink hover:text-ink-2 transition-colors focus-mono cursor-pointer"
        >
          <Plus size={14} />
          Publish your first transcript
        </Link>
      </div>
    </div>
  ) : (
    <TeachingEmptyState
      icon={FolderOpen}
      title="nothing published yet"
      body="this contributor has no public transcripts in the commons."
      privacy={null}
      style={{ border: "none", background: "transparent" }}
    />
  );

  // ── Library panel (main canvas) ──────────────────────────────────────────────
  // Header always visible. Loading: existing shimmer (keeps the card structure
  // consistent). Loaded: DataState discriminates empty vs content.
  const libraryPanel = (
    <div className="border border-rule bg-surface">
      <div className="flex items-center justify-between px-5 py-3 border-b border-rule">
        <span className="inline-flex items-center gap-2 text-sm font-medium text-ink">
          <Library size={14} className="text-ink-3" />
          Published library
        </span>
        {isOwnProfile && (
          <button
            type="button"
            onClick={() => setImportOpen(true)}
            className={cn(
              "inline-flex items-center gap-1.5 border border-rule bg-surface px-2.5 h-8",
              "text-[13px] font-medium text-ink",
              "hover:bg-surface-hover focus-mono transition-colors cursor-pointer",
            )}
          >
            <Upload className="size-3.5" />
            Import
          </button>
        )}
      </div>

      {isLoading ? (
        <div className="divide-y divide-rule">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="px-5 py-4">
              <div className="h-4 w-32 animate-shimmer" />
              <div className="mt-3 space-y-2">
                <div className="h-9 animate-shimmer" />
                <div className="h-9 animate-shimmer" />
              </div>
            </div>
          ))}
        </div>
      ) : (
        <DataState
          empty={groups.length === 0}
          emptyState={libraryEmptyState}
        >
          <div className="divide-y divide-rule">
            {groups.map((group, gi) => (
              <div
                key={group.project}
                className={`animate-fade-up stagger-${Math.min(gi + 1, 6)}`}
              >
                <div className="flex items-center gap-3 px-5 py-3 bg-surface-hover">
                  {editingProject === group.project ? (
                    <input
                      ref={editInputRef}
                      type="text"
                      value={draftName}
                      maxLength={255}
                      disabled={renameProjectMutation.isPending}
                      onChange={(e) => setDraftName(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          commitEdit();
                        } else if (e.key === "Escape") {
                          e.preventDefault();
                          cancelEdit();
                        }
                      }}
                      onBlur={commitEdit}
                      className="min-w-0 flex-1 max-w-xs bg-surface border border-rule px-2 py-0.5 font-[family-name:var(--font-display)] text-sm font-semibold text-ink focus-mono disabled:opacity-50"
                    />
                  ) : (
                    <h2 className="font-[family-name:var(--font-display)] text-sm font-semibold text-ink truncate">
                      {group.project}
                    </h2>
                  )}
                  <span className="font-mono text-xs text-ink-3 tabular-nums shrink-0">
                    {group.items.length} session
                    {group.items.length !== 1 ? "s" : ""}
                  </span>
                  <div className="flex-1" />
                  {isOwnProfile && editingProject !== group.project && (
                    <button
                      type="button"
                      aria-label="Rename project"
                      disabled={renameProjectMutation.isPending}
                      onClick={() => startEdit(group.project)}
                      className="inline-flex items-center gap-1 font-mono text-xs text-ink-3 hover:text-ink transition-colors cursor-pointer focus-mono disabled:opacity-50"
                    >
                      <Pencil size={12} />
                      Rename
                    </button>
                  )}
                </div>
                <TranscriptList
                  items={group.items}
                  showOwnerActions={isOwnProfile}
                  bare
                />
              </div>
            ))}
          </div>
        </DataState>
      )}
    </div>
  );

  // ── Rail — own profile only ──────────────────────────────────────────────────
  // Privacy toggle + account ops in two RailSections. Null for viewers: no rail
  // content means no RailShell (avoids a blank 320 px column beside the library).
  const profileRail =
    isOwnProfile && user ? (
      <>
        <RailSection title="privacy" icon={EyeOff} meta={undefined}>
          <div className="flex flex-col gap-4">
            <div>
              <p className="text-sm font-medium text-ink">Discoverable profile</p>
              <p className="text-[13px] text-ink-3 mt-1 leading-relaxed">
                When off, you do not appear in collective member or contributor
                lists, and your shared transcripts are attributed to{" "}
                <span className="font-mono">anon</span> instead of your handle.
              </p>
            </div>
            <label
              className={cn(
                "inline-flex items-center gap-2 cursor-pointer select-none",
                updateSettingsMutation.isPending && "opacity-50 cursor-wait",
              )}
            >
              <span className="font-mono text-xs text-ink-3 tabular-nums">
                {user.is_discoverable ? "ON" : "OFF"}
              </span>
              <input
                type="checkbox"
                checked={user.is_discoverable}
                disabled={updateSettingsMutation.isPending}
                onChange={(e) =>
                  updateSettingsMutation.mutate({
                    is_discoverable: e.target.checked,
                  })
                }
                className="h-4 w-4 border border-rule bg-surface focus-mono cursor-pointer disabled:cursor-wait"
              />
            </label>
          </div>
        </RailSection>

        <RailSection title="account" icon={AlertTriangle} meta={undefined}>
          <div className="flex flex-col gap-3">
            <p className="text-[13px] text-ink-3 leading-relaxed">
              Permanently delete your account. This also removes your published
              transcripts and the collectives you own. This cannot be undone.
            </p>
            <Button
              variant="danger"
              size="sm"
              icon={Trash2}
              disabled={deleteAccountMutation.isPending}
              onClick={deleteAccount}
            >
              {deleteAccountMutation.isPending ? "Deleting…" : "Delete account"}
            </Button>
          </div>
        </RailSection>
      </>
    ) : null;

  return (
    <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
      {/* Breadcrumb */}
      <nav className="flex items-center gap-1.5 text-xs">
        <Link
          href="/"
          className="text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
        >
          Commons
        </Link>
        <ChevronRight size={12} strokeWidth={2} className="text-ink-4" />
        <span className="text-ink font-medium">@{username}</span>
      </nav>

      {/* Profile hero — square avatar, display name, @handle */}
      <section className="flex items-start gap-4">
        {avatarUrl ? (
          <img
            src={avatarUrl}
            alt=""
            className="h-16 w-16 shrink-0 border border-rule object-cover"
          />
        ) : (
          <div className="h-16 w-16 shrink-0 border border-rule bg-surface flex items-center justify-center font-[family-name:var(--font-display)] text-2xl font-semibold text-ink-3">
            {username[0].toUpperCase()}
          </div>
        )}
        <div className="min-w-0 flex flex-col gap-1.5">
          <h1 className="font-[family-name:var(--font-display)] text-2xl font-semibold tracking-tight text-ink truncate">
            {displayName}
          </h1>
          <p className="font-mono text-sm text-ink-3">@{username}</p>
        </div>
      </section>

      {/* KPI tiles — StatGrid (transcripts + projects), shown once data lands */}
      {data && !isLoading && (
        <StatGrid
          tiles={[
            {
              key: "transcripts",
              label: "transcripts",
              value: data.total.toLocaleString(),
              icon: FileText,
            },
            {
              key: "projects",
              label: "projects",
              value: groups.length.toLocaleString(),
              icon: FolderOpen,
            },
          ]}
        />
      )}

      {/*
       * Library + profile settings.
       *
       * Own profile  → RailShell: library as scrolling canvas, privacy toggle +
       *   account ops in a 320 px sticky rail. On mobile the rail collapses into a
       *   fixed bottom-sheet toggled by "profile settings".
       *
       * Viewer profile → library standalone, no rail column.
       */}
      {profileRail ? (
        <RailShell
          toolbar={undefined}
          sheetMeta={undefined}
          sheetTitle="profile settings"
          rail={profileRail}
        >
          {libraryPanel}
        </RailShell>
      ) : (
        libraryPanel
      )}

      <PublishImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
      />
    </div>
  );
}
