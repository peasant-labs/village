"use client";

import { use, useState } from "react";
import Image from "next/image";
import { useTranscripts } from "@/lib/queries/transcripts";
import { useGroupedTranscriptsPaged } from "@/lib/queries/helperGroups";
import {
  ScopedContextContainerList,
  ScopedOwnerHelperGroups,
  helperGroupsByTranscript,
} from "@/components/transcript/ScopedHelperGroups";
import ScopedGroupedContinuation from "@/components/transcript/ScopedGroupedContinuation";
import { useDeleteAccount, usePublicProfile, useUpdateMySettings } from "@/lib/queries/auth";
import { usePublicOrgs } from "@/lib/queries/orgs";
import { useAuth } from "@/providers/AuthProvider";
import TranscriptList from "@/components/transcript/TranscriptList";
import AgentSessionGroup from "@/components/transcript/AgentSessionGroup";
import {
  Button,
  DataState,
  RailSection,
  RailShell,
  StatGrid,
  TeachingEmptyState,
} from "@/lib/ft-ui";
import PublishImportDialog from "@/app/publish/PublishImportDialog";
import ProfileCollectives from "@/components/collective/ProfileCollectives";
import MalformedProjectNotice from "@/components/MalformedProjectNotice";
import { groupByProject } from "@/lib/format";
import { childSessionsByParentID, groupChildSessions } from "@/lib/childSessions";
import { cn } from "@/lib/utils";
import {
  AlertTriangle,
  EyeOff,
  FileText,
  FolderOpen,
  Library,
  Plus,
  Trash2,
  Upload,
} from "lucide-react";
import Link from "next/link";
import { OnCrumb, OnEmpty, OnPage } from "@/components/online/Page";
import { HOME_SECTION } from "@/lib/nav/sections";

/**
 * The trail above a profile. A signed-in reader starts it at their own home;
 * a signed-out visitor has no home here, so the profile stands on its own.
 * Nothing on this page leads to the discovery list, which is hidden.
 */
function ProfileCrumb({ username, signedIn }: { username: string; signedIn: boolean }) {
  if (!signedIn) return null;
  return (
    <OnCrumb items={[{ label: HOME_SECTION.label, href: HOME_SECTION.href }, { label: `@${username}` }]} />
  );
}

export default function UserProfilePage({
  params,
}: {
  params: Promise<{ username: string }>;
}) {
  const { username } = use(params);
  const { user, isLoggedIn } = useAuth();
  const { data: profile, isError: isProfileError } = usePublicProfile(username);
  // The organizations this person has linked. The server sends only the
  // visible ones, and answers the same way for an owner and a visitor.
  const { data: linkedOrgs } = usePublicOrgs(username);
  const publicOrgs = linkedOrgs ?? [];
  const { data, isLoading } = useTranscripts({ owner: username });
  // The server's own helper-group fold for this person's library. Independent
  // of the flat list above: the rows, the ordinary child chip and the project
  // bucketing are unchanged, and a grouped read that fails contributes nothing.
  // The grouped read pages, so an owner or helper-only context container past
  // the first grouped page still reaches its grouped exit.
  const grouped = useGroupedTranscriptsPaged({ owner: username });
  const groupedItems = grouped.items;
  const helperGroups = helperGroupsByTranscript(groupedItems);
  const refreshGrouped = grouped.refreshOrigin;
  const deleteAccountMutation = useDeleteAccount();
  const updateSettingsMutation = useUpdateMySettings();
  const [importOpen, setImportOpen] = useState(false);

  const isOwnProfile =
    !!user && user.github_username.toLowerCase() === username.toLowerCase();
  // The attachment preference is served by /users/me/settings, not /auth/me, so
  // it is read only for the caller's own profile and never for a visitor.
  const { groups, malformed: malformedProjectItems } = data?.transcripts
    ? groupByProject(data.transcripts)
    : { groups: [], malformed: [] };
  const agentTotal = data?.agent_total ?? 0;

  // Fall back to the session user for one's own profile so the owner is never
  // locked out of their settings even if the public profile fetch fails.
  const effectiveProfile = profile ?? (isOwnProfile ? user : undefined);
  const displayName = effectiveProfile?.display_name ?? username;
  const avatarUrl = effectiveProfile?.avatar_url ?? undefined;

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
      <OnPage tight={isLoggedIn}>
        <ProfileCrumb username={username} signedIn={isLoggedIn} />
        <OnEmpty
          icon={<EyeOff aria-hidden="true" />}
          title="user not found"
          body="there is no profile here that you can open."
          testId="profile-not-found"
          actions={
            isLoggedIn ? (
              <Link href={HOME_SECTION.href} className="btn btn-secondary">
                back to home
              </Link>
            ) : undefined
          }
        />
      </OnPage>
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
        body="the sessions you publish from peasant show up here."
        privacy={null}
        style={{ border: "none", background: "transparent" }}
      />
      <div className="px-6 pb-6">
        <Link href="/publish" className="on-link inline-flex items-center gap-1.5">
          <Plus size={14} aria-hidden="true" />
          how to publish from peasant
        </Link>
      </div>
    </div>
  ) : (
    <TeachingEmptyState
      icon={FolderOpen}
      title="nothing published yet"
      body={`@${username} has published nothing here that you can open.`}
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
        <span className="inline-flex items-center gap-2 font-mono text-[length:var(--fs-sm)] font-semibold text-[color:var(--ink-strong)]">
          <Library size={16} className="text-ink-3" aria-hidden="true" />
          published library
        </span>
        {isOwnProfile && (
          <button
            type="button"
            onClick={() => setImportOpen(true)}
            className="btn btn-secondary btn-sm"
          >
            <Upload className="size-3.5" aria-hidden="true" />
            import
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
        <>
          {/* Shared with the home page, which reports the same violation; the
              component's own doc carries the reasoning. */}
          <MalformedProjectNotice
            count={malformedProjectItems.length}
            testId="profile-malformed-notice"
            className="mb-3"
          />
          <DataState
            // A library made only of agent-driven sessions is not empty: the
            // collapsed group below is its whole content, so the teaching empty
            // state would be wrong and would hide it.
            empty={groups.length === 0 && agentTotal === 0}
            emptyState={libraryEmptyState}
          >
          <div className="divide-y divide-rule">
            {groups.map((group, gi) => {
              // A person's library is where they ask what a session did, so a
              // session another session started is listed under the one that
              // started it rather than beside it. Grouped WITHIN the project,
              // which is the only place a starter and what it started can both
              // be: `local_id` names a session of this owner's, and the rows
              // are already bucketed by project here.
              //
              // The project's own count still counts every transcript in it,
              // including the ones inside a chip. It answers how much the
              // project holds, which is not changed by where a row is drawn.
              const grouping = groupChildSessions(group.items);
              const childSessions = childSessionsByParentID(grouping);
              return (
              <div
                key={group.project_hash}
                className={`animate-fade-up stagger-${Math.min(gi + 1, 6)}`}
              >
                <div className="flex items-center gap-3 px-5 py-3 bg-surface-hover">
                  {/* The project name is the way into the project's own page,
                      where its transcripts, its collectives and (for the owner)
                      its name live. Routed on project_hash: the project's
                      identity, never a name that could be re-derived.
                      `normal-case` is load-bearing: the design system lowercases
                      h1/h2/h3 as UI chrome, and a project's display name is
                      USER CONTENT, which is never lowercased. */}
                  <Link
                    href={`/users/${encodeURIComponent(username)}/projects/${group.project_hash}`}
                    className="min-w-0 focus-mono cursor-pointer"
                  >
                    <h2 className="font-[family-name:var(--font-display)] text-sm font-semibold text-ink truncate normal-case hover:text-ink-2 transition-colors">
                      {group.project}
                    </h2>
                  </Link>
                  <span className="font-mono text-xs text-ink-3 tabular-nums shrink-0">
                    {group.items.length} session
                    {group.items.length !== 1 ? "s" : ""}
                  </span>
                  <div className="flex-1" />
                </div>
                <TranscriptList
                  items={grouping.rootItems}
                  childSessions={childSessions}
                  showOwnerActions={isOwnProfile}
                  bare
                  helperGroupSlot={(item) => (
                    <ScopedOwnerHelperGroups
                      groups={helperGroups.get(item.transcript.id)}
                      onRefreshOrigin={refreshGrouped}
                    />
                  )}
                />
              </div>
              );
            })}
            {/* Helper-only containers carry no owner row, so they sit after
                every project group rather than inside one. */}
            <ScopedContextContainerList
              items={groupedItems}
              onRefreshOrigin={refreshGrouped}
            />
            {/* A grouped page holds at most one server page of top-level units;
                the continuation reads the next one so a later owner or
                helper-only container still gets its grouped exit. */}
            <ScopedGroupedContinuation
              remaining={grouped.remainingItems}
              busy={grouped.isFetchingNextPage}
              onLoadMore={() => void grouped.fetchNextPage()}
            />
            {/* Agent-driven sessions are never mixed into the project groups:
                they are not the work this person wrote, and grouping them by
                project would bury the sessions that are. They sit after every
                project as one collapsed, labelled group. */}
            <AgentSessionGroup
              agentTotal={agentTotal}
              baseParams={{ owner: username }}
              showOwnerActions={isOwnProfile}
              bare
            />
          </div>
          </DataState>
        </>
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
              <p className="text-sm font-medium text-ink">discoverable profile</p>
              <p className="text-[13px] text-ink-3 mt-1 leading-relaxed">
                when off, you do not appear in collective member or contributor
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
                {user.is_discoverable ? "on" : "off"}
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
              permanently delete your account. this also removes your published
              transcripts and the collectives you own. this cannot be undone.
            </p>
            <Button
              variant="danger"
              size="sm"
              icon={Trash2}
              disabled={deleteAccountMutation.isPending}
              onClick={deleteAccount}
            >
              {deleteAccountMutation.isPending ? "deleting" : "delete account"}
            </Button>
          </div>
        </RailSection>
      </>
    ) : null;

  return (
    <OnPage tight={isLoggedIn} testId="profile-page">
      <ProfileCrumb username={username} signedIn={isLoggedIn} />

      {/* Profile hero: square avatar, display name, @handle */}
      <header className="on-head">
        <div className="on-title-row">
          {avatarUrl ? (
            // unoptimized: a provider-supplied avatar host (see the images note
            // in next.config.ts).
            <Image
              src={avatarUrl}
              alt=""
              width={64}
              height={64}
              unoptimized
              className="h-16 w-16 shrink-0 border border-rule object-cover"
            />
          ) : (
            <div
              aria-hidden="true"
              className="h-16 w-16 shrink-0 border border-rule bg-surface flex items-center justify-center font-[family-name:var(--font-display)] text-2xl font-semibold text-ink-3"
            >
              {username[0].toUpperCase()}
            </div>
          )}
          <div className="min-w-0 flex flex-col gap-1.5">
            {/* A person's display name is USER CONTENT. `normal-case` overrides the
                design system's h1/h2/h3 lowercasing, which is a chrome rule. */}
            <h1 className="on-title normal-case">{displayName}</h1>
            <p className="on-chrome m-0">@{username}</p>
          </div>
        </div>
      </header>

      {/*
       * Linked GitHub organizations. Only the organizations a person has linked
       * appear here; the server never sends an unlinked one. The whole section
       * is omitted when there is nothing to show, so an unlinked profile gains
       * no empty box where a different profile lists its organizations.
       */}
      {publicOrgs.length > 0 && (
        <section className="flex flex-col gap-2" data-testid="profile-orgs">
          <h2 className="on-label">organizations</h2>
          <ul className="flex flex-wrap items-center gap-2">
            {publicOrgs.map((org) => (
              <li
                key={org.org_login}
                data-testid="profile-org"
                className="inline-flex items-center gap-2 border border-rule bg-surface px-2.5 h-8"
              >
                {org.avatar_url ? (
                  // unoptimized: a provider-supplied avatar host (see the
                  // images note in next.config.ts).
                  <Image
                    src={org.avatar_url}
                    alt=""
                    width={24}
                    height={24}
                    unoptimized
                    data-testid="profile-org-avatar"
                    className="size-6 border border-rule object-cover shrink-0"
                  />
                ) : null}
                {/* An organization login is USER CONTENT, never lowercased. */}
                <span className="font-mono text-[13px] text-ink normal-case">
                  {org.org_login}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

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

      {/* Contributed collectives — own profile only. The section itself owns
       * that gate (and disables its own request with it), so there is one
       * place it can be got wrong rather than two. */}
      <ProfileCollectives isOwnProfile={isOwnProfile} />

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
    </OnPage>
  );
}
