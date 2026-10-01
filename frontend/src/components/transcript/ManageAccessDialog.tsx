"use client";

import { useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import { useQueries } from "@tanstack/react-query";
import type { VillageUserGroupShare } from "@peasant-labs/schema";
import { api } from "@/lib/api";
import ConfirmContributeDialog from "./ConfirmContributeDialog";
import { AccessList, CollectivePicker, Dialog } from "@/lib/ft-ui";
import { useMyCollectiveContributions, useTranscriptCollectives } from "@/lib/queries/collectives";
import { useGroups } from "@/lib/queries/groups";
import { useShareTranscript, useTranscript, useUnshareTranscript } from "@/lib/queries/transcripts";

type AccessItem = ComponentProps<typeof AccessList>["items"][number];
type Suggestion = ComponentProps<typeof CollectivePicker>["suggestions"][number];

/** A note on a suggested collective that takes submissions differently from
 *  an open one, so the owner knows what `add` will do before pressing it. */
const ACCEPTANCE_NOTE: Record<string, string | undefined> = {
  curated: "curated · waits for the collective owner’s approval",
  verified_only: "verified members only",
};

type Change = { kind: "add" | "remove"; id: string; name: string };

interface ManageAccessDialogProps {
  open: boolean;
  onClose: () => void;
  transcriptId: string;
}

/**
 * Who can read this transcript, for its owner: the collectives it is shared
 * with, a remove button on each, and a picker to add another. Each change is
 * its own request on the existing share routes, and the list is re-read from
 * the server once the request settles, so it only ever shows what village
 * holds.
 *
 * - who can read it: `GET /transcripts/{id}/collectives` (approved
 *   submissions). Owner-only contribution reads supply recorded pending and
 *   accepted submissions, including collectives the owner has left.
 * - add: `POST /transcripts/{id}/share` with one collective.
 * - remove: `DELETE /transcripts/{id}/share/{groupID}`; its members lose access.
 */
export default function ManageAccessDialog({ open, onClose, transcriptId }: ManageAccessDialogProps) {
  const collectives = useTranscriptCollectives(transcriptId);
  const transcript = useTranscript(transcriptId);
  const groups = useGroups();
  const contributions = useMyCollectiveContributions(open);
  const liveGroups = (contributions.data ?? []).filter((g) => g.approved_count > 0 || g.pending_count > 0);
  const submissions = useQueries({
    queries: liveGroups.map((group) => ({
      queryKey: ["group-my-shares", group.id],
      queryFn: () => api<VillageUserGroupShare[]>(`/groups/${encodeURIComponent(group.id)}/my-shares`),
      enabled: open,
    })),
  });
  const share = useShareTranscript();
  const unshare = useUnshareTranscript();
  const doneRef = useRef<HTMLButtonElement>(null);
  const [query, setQuery] = useState("");
  const [change, setChange] = useState<Change | null>(null);
  const [failed, setFailed] = useState<Change | null>(null);
  const [consent, setConsent] = useState<Change | null>(null);
  const [recorded, setRecorded] = useState<boolean | null>(null);

  // Replacing one modal with the other runs its focus-return frame. Move focus
  // after that frame, and only when it ended outside the current modal.
  useEffect(() => {
    if (!open) return;
    let followup: number | undefined;
    const frame = requestAnimationFrame(() => {
      followup = requestAnimationFrame(() => {
        const label = consent ? "cns-contribute" : "manage-access-title";
        const dialog = document.getElementById(label)?.closest<HTMLElement>('[role="dialog"]');
        if (dialog && !dialog.contains(document.activeElement)) {
          (dialog.querySelector<HTMLElement>('button:not([disabled]), input:not([disabled])') ?? dialog).focus();
        }
      });
    });
    return () => {
      cancelAnimationFrame(frame);
      if (followup !== undefined) cancelAnimationFrame(followup);
    };
  }, [open, consent]);

  const memberCounts = useMemo(
    () => new Map((groups.data ?? []).map((g) => [g.id, g.member_count])),
    [groups.data],
  );

  const items: AccessItem[] = (collectives.data ?? []).map((c) => ({
    id: c.id,
    name: c.name,
    members: memberCounts.get(c.id),
  }));
  for (let index = 0; index < liveGroups.length; index++) {
    const group = liveGroups[index];
    const row = submissions[index].data?.find((s) => s.id === transcriptId);
    if (!row || (row.status !== "approved" && row.status !== "pending")) continue;
    const at = items.findIndex((item) => item.id === group.id);
    const item: AccessItem = {
      id: group.id,
      name: group.name,
      members: memberCounts.get(group.id),
      ...(row.status === "pending" ? { pending: "approval" as const } : {}),
    };
    if (at < 0) items.push(item);
    else items[at] = item;
  }

  const suggestions: Suggestion[] = (() => {
    const present = new Set(items.map((item) => item.id));
    const needle = query.trim().toLowerCase();
    return (groups.data ?? [])
      .filter((g) => g.role && g.role !== "pending" && !present.has(g.id))
      .filter((g) => !needle || g.name.toLowerCase().includes(needle))
      .map((g) => ({
        id: g.id,
        name: g.name,
        members: g.member_count ?? undefined,
        note: ACCEPTANCE_NOTE[g.acceptance_mode],
      }));
  })();

  const busy = share.isPending || unshare.isPending;

  function submit(next: Change) {
    setConsent(null);
    setChange(next);
    setFailed(null);
    setRecorded(null);
    setQuery("");
    share.mutate({ transcriptId, groupId: next.id }, {
      onSuccess: (shares) => setRecorded(shares.some((s) => s.group_id === next.id)),
      onError: () => setFailed(next),
    });
  }

  function add(id: string) {
    if (busy) return;
    const name = groups.data?.find((g) => g.id === id)?.name ?? "that collective";
    const next: Change = { kind: "add", id, name };
    if (transcript.data?.transcript.visibility === "private") setConsent(next);
    else submit(next);
  }

  function remove(id: string) {
    if (busy) return;
    const name = items.find((item) => item.id === id)?.name ?? "that collective";
    const next: Change = { kind: "remove", id, name };
    setChange(next);
    setFailed(null);
    unshare.mutate({ transcriptId, groupId: id }, {
      onSuccess: () => doneRef.current?.focus(),
      onError: () => setFailed(next),
    });
  }

  // The POST response names recorded submissions; an omitted collective was
  // skipped by the server. Read failures never decide this outcome.
  let status: string | null = null;
  if (change && busy) {
    status = change.kind === "add" ? `adding ${change.name}…` : `removing ${change.name}…`;
  } else if (change && !failed) {
    status = change.kind === "remove"
      ? `removed ${change.name}.`
      : recorded === false
        ? `${change.name} did not record a submission.`
        : recorded === true ? `submitted to ${change.name}.` : null;
  }

  const visibility = transcript.data?.transcript.visibility;
  const readFailed = collectives.isError || contributions.isError || transcript.isError || submissions.some((q) => q.isError);
  const loading = collectives.isLoading || contributions.isLoading || transcript.isLoading || submissions.some((q) => q.isLoading);
  const ready = !readFailed && !loading;
  function retryReads() {
    void collectives.refetch();
    void contributions.refetch();
    void transcript.refetch();
    for (const read of submissions) void read.refetch();
  }

  return (
    <>
    <Dialog
      open={open && !consent}
      onClose={onClose}
      title="manage access"
      labelId="manage-access-title"
      className="pub-dialog"
      footer={
        <button type="button" className="btn btn-primary btn-sm" ref={doneRef} onClick={onClose}>
          done
        </button>
      }
    >
      <section className="pub-section" aria-labelledby="manage-access-readers">
        <h4 className="pub-section-title" id="manage-access-readers">
          who can read it
        </h4>
        {visibility === "public" && <p className="pub-line">anyone with the link can read it.</p>}
        {readFailed ? (
          <p className="pub-line pub-line-alert" role="alert">
            the collectives could not load.{" "}
            <button type="button" className="btn btn-ghost btn-sm" onClick={retryReads}>
              retry
            </button>
          </p>
        ) : loading ? (
          <p className="pub-line" role="status">
            loading who can read it…
          </p>
        ) : (
          <AccessList
            items={items}
            onRemove={remove}
            empty={visibility === "public" ? "no collective holds it." : "only you can read it."}
          />
        )}
        {groups.isError ? (
          <p className="pub-line pub-line-alert" role="alert">
            your collectives could not load.{" "}
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => void groups.refetch()}>retry</button>
          </p>
        ) : groups.isLoading ? (
          <p className="pub-line" role="status">loading your collectives…</p>
        ) : ready && !busy ? <CollectivePicker
          suggestions={suggestions}
          query={query}
          onQueryChange={setQuery}
          onAdd={add}
          empty="none of your collectives matches that name."
        /> : null}
        <p className="pub-hint">removing a collective takes the transcript back from it: its members lose access.</p>
        {/* Always mounted, so a screen reader hears each change as it lands. */}
        <p className={status ? "pub-line" : "sr-only"} role="status" aria-live="polite">
          {status}
        </p>
        {failed && (
          <p className="pub-line pub-line-alert" role="alert">
            {failed.kind === "add"
              ? `could not add ${failed.name}. review the list and try again.`
              : `could not remove ${failed.name}. review the list and try again.`}
          </p>
        )}
      </section>
    </Dialog>
    <ConfirmContributeDialog
      open={open && consent !== null}
      onClose={() => setConsent(null)}
      onConfirm={() => { if (consent) submit(consent); }}
      transcripts={[{ id: transcriptId, title: transcript.data?.transcript.title ?? "untitled transcript" }]}
      collectives={consent ? [{ id: consent.id, name: consent.name, memberCount: memberCounts.get(consent.id) }] : []}
      isSubmitting={busy}
    />
    </>
  );
}
