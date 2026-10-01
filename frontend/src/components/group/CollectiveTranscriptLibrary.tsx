"use client";
import { useState } from "react";
import Link from "next/link";
import { Trash2 } from "lucide-react";
import type { HelperGroupSummary } from "@peasant-labs/schema";
import type { GroupTranscript, UserGroupShare } from "@/lib/types";
import { Checkbox, SessionGroupDisclosure } from "@/lib/ft-ui";
import { useAuth } from "@/providers/AuthProvider";
import { useMyGroupShares, useRemoveGroupTranscript } from "@/lib/queries/groups";
import { useUnshareTranscript } from "@/lib/queries/transcripts";
import { useGroupedCollective, useGroupedMyShares } from "@/lib/queries/groupedCollectives";
import { GROUPED_TOP_LEVEL_PAGE_SIZE } from "@/lib/queries/helperGroups";
import { collectiveTranscriptRow, resolveAttribution } from "@/lib/format";
import { type SessionIdentity, childSessionGroupLabel, childSessionsByParentID, childSessionsByRowID, groupChildSessions, groupSessionRows } from "@/lib/childSessions";
import TranscriptList, { type TranscriptRowFact } from "@/components/transcript/TranscriptList";
import CollectiveRepos from "@/components/group/CollectiveRepos";
import ProviderBadge from "@/components/transcript/ProviderBadge";
import { ScopedOwnerHelperGroups, ScopedUnownedHelperGroups, helperGroupsByTranscript, unownedGroupedItems } from "@/components/transcript/ScopedHelperGroups";
import ScopedGroupedContinuation from "@/components/transcript/ScopedGroupedContinuation";
/** One of the caller's own contributions to a collective. */
function MyContributionRow({ share, groupID, onUnshare, unsharing, helperGroups, onRefreshOrigin, }: {
    share: UserGroupShare;
    groupID: string;
    onUnshare: (input: {
        transcriptId: string;
        groupId: string;
    }) => void;
    unsharing: boolean;
    /** The saved helper groups the server grouped under this contribution. */
    helperGroups?: readonly HelperGroupSummary[];
    /** Refresh of the originating grouped list, offered by an expired scope. */
    onRefreshOrigin: () => void;
}) {
    const row = (<div className="flex items-center gap-3 px-5 py-2.5 hover:bg-surface-hover transition-colors">
      <ProviderBadge provider={share.model_provider}/>
      <Link href={`/transcripts/${share.id}`} className="text-[var(--fs-body)] text-ink truncate min-w-0 flex-1 hover:underline focus-mono cursor-pointer">
        {share.title || "untitled"}
      </Link>
      {share.status === "pending" && (<span className="text-sm font-mono text-ink-3 tracking-wider shrink-0">
          pending
        </span>)}
      <span className="text-sm font-mono text-ink-3 tabular-nums shrink-0">
        {new Date(share.shared_at).toLocaleDateString("en-US", {
            month: "short",
            day: "numeric",
        })}
      </span>
      <button type="button" onClick={() => onUnshare({ transcriptId: share.id, groupId: groupID })} disabled={unsharing} title="withdraw contribution" className="inline-flex size-7 items-center justify-center border border-rule bg-surface text-ink-3 hover:bg-danger-soft hover:text-danger focus-mono transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed shrink-0">
        <Trash2 className="size-3.5"/>
      </button>
    </div>);
    // A contribution the server grouped helpers under reads them beneath its own
    // row, exactly like every other collective surface. A contribution with no
    // saved helpers is drawn as the row alone, so nothing else on this list
    // changes shape.
    if (helperGroups == null || helperGroups.length === 0)
        return row;
    return (<div data-helper-group-owner={share.id}>
      {row}
      <ScopedOwnerHelperGroups groups={helperGroups} onRefreshOrigin={onRefreshOrigin}/>
    </div>);
}
/**
 * The contributions one contribution started, behind the shared collapsed
 * control. Nothing renders when the row above started nothing in this response.
 *
 * Collapse state lives here, one instance per parent row, because a group is an
 * aside: a person who opened one has not asked for every one to be open.
 */
function MyContributionChildren({ parentShareID, startedShares, groupID, onUnshare, unsharing, helperGroupsByRowID, onRefreshOrigin, }: {
    parentShareID: string;
    startedShares: UserGroupShare[];
    groupID: string;
    onUnshare: (input: {
        transcriptId: string;
        groupId: string;
    }) => void;
    unsharing: boolean;
    helperGroupsByRowID: ReadonlyMap<string, HelperGroupSummary[]>;
    onRefreshOrigin: () => void;
}) {
    const [expanded, setExpanded] = useState(false);
    const rowsID = `my-contribution-children-${parentShareID}`;
    if (startedShares.length === 0)
        return null;
    return (<div data-parent-transcript-id={parentShareID}>
      <SessionGroupDisclosure label={childSessionGroupLabel(startedShares.length)} collapsedLabel={childSessionGroupLabel(startedShares.length)} expanded={expanded} onToggle={() => setExpanded((open) => !open)} rowsID={rowsID} testID="child-session-disclosure" bare indent>
        <div id={rowsID} data-testid="child-session-disclosure-rows" className="border-t border-rule divide-y divide-rule">
          {startedShares.map((child) => (<MyContributionRow key={child.id} share={child} groupID={groupID} onUnshare={onUnshare} unsharing={unsharing} helperGroups={helperGroupsByRowID.get(child.id)} onRefreshOrigin={onRefreshOrigin}/>))}
        </div>
      </SessionGroupDisclosure>
    </div>);
}
/** The shared fold's four facts, read out of one of the caller's own
 *  contributions. Its row identity is the transcript id under a different
 *  name -- this response calls it `id` where the pending queue calls it
 *  `transcript_id` -- which is exactly why each list states its own reading
 *  instead of the fold guessing at a field name. */
function myShareIdentity(share: UserGroupShare): SessionIdentity {
    return {
        rowID: share.id,
        ownerID: share.owner_id,
        sessionID: share.local_id,
        parentSessionID: share.parent_session_id,
    };
}
/**
 * What a row states on a collective's browse list: everything its table stated
 * as a column, in the order every other transcript list in this app reads.
 */
const COLLECTIVE_BROWSE_FACTS: readonly TranscriptRowFact[] = [
    "provider",
    "date",
    "turns",
    "tokens",
];
/** Retained production exits alongside the canonical collective body. The
 * server's existing flat rows and opaque grouped scopes remain authoritative. */
export default function CollectiveTranscriptLibrary({ groupID, transcripts, canRead, isOwner }: {
    groupID: string;
    transcripts: GroupTranscript[];
    canRead: boolean;
    isOwner: boolean;
}) {
    const { user } = useAuth();
    const [expanded, setExpanded] = useState(false);
    const [view, setView] = useState<"list" | "repos">("list");
    const [contributor, setContributor] = useState("");
    const [selected, setSelected] = useState<Set<string>>(new Set());
    const [removing, setRemoving] = useState(false);
    const [removeError, setRemoveError] = useState<string | null>(null);
    const grouped = useGroupedCollective(groupID, { limit: GROUPED_TOP_LEVEL_PAGE_SIZE }, canRead);
    const myShares = useMyGroupShares(groupID, !!user);
    const groupedMine = useGroupedMyShares(groupID, { limit: GROUPED_TOP_LEVEL_PAGE_SIZE }, !!user);
    const withdraw = useUnshareTranscript();
    const remove = useRemoveGroupTranscript();
    const attribution = (row: GroupTranscript) => resolveAttribution({ id: row.owner_id, github_username: row.owner_username, is_discoverable: row.owner_is_discoverable }, user?.id, isOwner);
    const contributors = new Map(transcripts.map((row) => { const owner = attribution(row); return [owner.anonymous ? "anonymous" : row.owner_id, owner.label]; }));
    const visible = contributor ? transcripts.filter((row) => { const owner = attribution(row); return (owner.anonymous ? "anonymous" : row.owner_id) === contributor; }) : transcripts;
    const folded = groupChildSessions(visible.map(collectiveTranscriptRow));
    const children = childSessionsByParentID(folded);
    // Include folded children in the represented set; their helper slots live
    // under those rows, and the fallback must not draw them a second time.
    const represented = new Set(transcripts.map((row) => row.id));
    const helpers = helperGroupsByTranscript(grouped.items);
    const fallback = unownedGroupedItems(grouped.items, represented);
    const shares = myShares.data ?? [];
    const mine = groupSessionRows(shares, myShareIdentity);
    const myChildren = childSessionsByRowID(mine, myShareIdentity);
    const representedMine = new Set(shares.map((row) => row.id));
    const myHelpers = helperGroupsByTranscript(groupedMine.items);
    const myFallback = unownedGroupedItems(groupedMine.items, representedMine);
    async function removeSelected() {
        setRemoving(true);
        setRemoveError(null);
        const failed = new Set<string>();
        for (const transcriptId of selected) {
            if (!transcripts.some((row) => row.id === transcriptId))
                continue;
            try {
                await remove.mutateAsync({ groupId: groupID, transcriptId });
            }
            catch (error) {
                failed.add(transcriptId);
                setRemoveError(error instanceof Error ? error.message : "the request failed");
            }
        }
        setSelected(failed);
        setRemoving(false);
    }
    function retry(query: {
        error: Error | null;
        isFetching: boolean;
        refetch: () => unknown;
    }) {
        return <div role="alert" className="text-[var(--fs-body)] text-danger px-5 py-3"><p>could not read saved transcript groups: {query.error?.message}</p><button type="button" className="btn btn-secondary btn-sm" disabled={query.isFetching} onClick={() => void query.refetch()}>try again</button></div>;
    }
    return <div className="mx-auto w-full max-w-[1152px] flex flex-col gap-4" data-testid="collective-transcript-library">
    {canRead && (transcripts.length > 0 || fallback.length > 0 || grouped.remainingItems > 0 || grouped.isError) && <div className="border border-rule bg-surface">
      <SessionGroupDisclosure label="transcript groups" collapsedLabel="transcript groups" expanded={expanded} onToggle={() => setExpanded((open) => !open)} rowsID={`collective-library-${groupID}`} testID="collective-library-disclosure" bare>
        <div id={`collective-library-${groupID}`}>
          {transcripts.length > 0 && <div className="flex items-center justify-between gap-3 px-5 py-3 border-b border-rule">
            <label className="text-sm font-mono text-ink-3">contributor <select aria-label="filter transcripts by contributor" value={contributor} onChange={(event) => setContributor(event.target.value)} className="bg-surface border border-rule text-sm text-ink ml-2"><option value="">all contributors</option>{[...contributors].map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select></label>
            <div className="flex items-center gap-2"><button type="button" className="btn btn-ghost btn-sm" aria-pressed={view === "list"} onClick={() => setView("list")}>list</button><button type="button" className="btn btn-ghost btn-sm" aria-pressed={view === "repos"} onClick={() => setView("repos")}>repos</button></div>
            {isOwner && <><Checkbox checked={visible.length > 0 && visible.every((row) => selected.has(row.id))} disabled={removing} onChange={(checked) => setSelected((current) => { const next = new Set(current); for (const row of visible) {
                if (checked)
                    next.add(row.id);
                else
                    next.delete(row.id);
            } return next; })}>select all</Checkbox><button type="button" className="btn btn-secondary btn-sm tabular-nums" disabled={removing || selected.size === 0} onClick={() => void removeSelected()}>{removing ? "removing" : `remove selected (${selected.size})`}</button></>}
          </div>}
          {removeError && <p role="alert" className="text-[var(--fs-body)] text-danger px-5 py-3">could not remove all selected contributions: {removeError}. failed rows remain selected; try again.</p>}
          {view === "repos" ? <CollectiveRepos transcripts={visible} viewerIsOwner={isOwner} helperGroups={helpers} onRefreshOrigin={grouped.refreshOrigin}/> : <TranscriptList items={folded.rootItems} childSessions={children} facts={COLLECTIVE_BROWSE_FACTS} selection={isOwner ? { selectedIDs: selected, onToggle: (id) => { if (removing)
                return; setSelected((current) => { const next = new Set(current); if (next.has(id))
                next.delete(id);
            else
                next.add(id); return next; }); } } : undefined} viewerIsPrivileged={isOwner} linkOwner bare helperGroupSlot={(item) => <ScopedOwnerHelperGroups groups={helpers.get(item.transcript.id)} onRefreshOrigin={grouped.refreshOrigin}/>}/>}
          {grouped.isError && retry(grouped)}
          {(fallback.length > 0 || grouped.remainingItems > 0) && <div data-testid="grouped-helper-fallback" className="border-t border-rule"><ScopedUnownedHelperGroups items={grouped.items} representedOwnerIds={represented} onRefreshOrigin={grouped.refreshOrigin}/><ScopedGroupedContinuation remaining={grouped.remainingItems} busy={grouped.isFetchingNextPage} onLoadMore={() => void grouped.fetchNextPage()}/></div>}
        </div>
      </SessionGroupDisclosure>
    </div>}
    {user && (shares.length > 0 || myFallback.length > 0 || groupedMine.remainingItems > 0 || myShares.isError || groupedMine.isError) && <div className="border border-rule bg-surface" data-testid="my-contributions-panel">
      <div className="flex justify-between px-5 py-3 border-b border-rule"><span className="text-[var(--fs-body)] text-ink">your contributions</span><span className="font-mono text-sm text-ink-3 tabular-nums">{shares.length}</span></div>
      {myShares.isError && retry(myShares)}
      {withdraw.isError && <p role="alert" className="text-[var(--fs-body)] text-danger px-5 py-3">could not withdraw the contribution: {withdraw.error.message}. try again.</p>}
      <div className="divide-y divide-rule">{mine.rootItems.map((share) => <div key={share.id}><MyContributionRow share={share} groupID={groupID} onUnshare={withdraw.mutate} unsharing={withdraw.isPending} helperGroups={myHelpers.get(share.id)} onRefreshOrigin={groupedMine.refreshOrigin}/><MyContributionChildren parentShareID={share.id} startedShares={myChildren.get(share.id) ?? []} groupID={groupID} onUnshare={withdraw.mutate} unsharing={withdraw.isPending} helperGroupsByRowID={myHelpers} onRefreshOrigin={groupedMine.refreshOrigin}/></div>)}</div>
      {groupedMine.isError && retry(groupedMine)}
      {(myFallback.length > 0 || groupedMine.remainingItems > 0) && <div data-testid="grouped-helper-fallback" className="border-t border-rule"><ScopedUnownedHelperGroups items={groupedMine.items} representedOwnerIds={representedMine} onRefreshOrigin={groupedMine.refreshOrigin}/><ScopedGroupedContinuation remaining={groupedMine.remainingItems} busy={groupedMine.isFetchingNextPage} onLoadMore={() => void groupedMine.fetchNextPage()}/></div>}
    </div>}
  </div>;
}
