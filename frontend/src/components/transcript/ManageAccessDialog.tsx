"use client";

import { useMemo, useState, type ComponentProps } from "react";
import { AccessList, CollectivePicker, Dialog } from "@/lib/ft-ui";
import { useTranscriptCollectives } from "@/lib/queries/collectives";
import { useGroups } from "@/lib/queries/groups";
import { useShareTranscript, useTranscript, useUnshareTranscript } from "@/lib/queries/transcripts";

type AccessItem = ComponentProps<typeof AccessList>["items"][number];
type Suggestion = ComponentProps<typeof CollectivePicker>["suggestions"][number];

/** A note on a suggested collective that takes submissions differently from
 *  an open one, so the owner knows what `add` will do before pressing it. */
const ACCEPTANCE_NOTE: Record<string, string | undefined> = {
  curated: "curated · waits for the owner’s approval",
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
 * - who can read it: `GET /transcripts/{id}/collectives` (accepted
 *   memberships), plus the pending submissions a curated collective has not
 *   decided yet, read from the transcript's own `enriched_shares`.
 * - add: `POST /transcripts/{id}/share` with one collective.
 * - remove: `DELETE /transcripts/{id}/share/{groupID}`; its members lose access.
 */
export default function ManageAccessDialog({ open, onClose, transcriptId }: ManageAccessDialogProps) {
  const collectives = useTranscriptCollectives(transcriptId);
  const transcript = useTranscript(transcriptId);
  const groups = useGroups();
  const share = useShareTranscript();
  const unshare = useUnshareTranscript();
  const [query, setQuery] = useState("");
  const [change, setChange] = useState<Change | null>(null);
  const [failed, setFailed] = useState<Change | null>(null);

  const memberCounts = useMemo(
    () => new Map((groups.data ?? []).map((g) => [g.id, g.member_count])),
    [groups.data],
  );

  const items = useMemo<AccessItem[]>(() => {
    const accepted = (collectives.data ?? []).map<AccessItem>((c) => ({
      id: c.id,
      name: c.name,
      members: memberCounts.get(c.id) ?? undefined,
    }));
    const acceptedIds = new Set(accepted.map((item) => item.id));
    const waiting = new Map<string, AccessItem>();
    for (const s of transcript.data?.enriched_shares ?? []) {
      if (s.status !== "pending" || acceptedIds.has(s.group_id)) continue;
      waiting.set(s.group_id, {
        id: s.group_id,
        name: s.group_name,
        members: memberCounts.get(s.group_id) ?? undefined,
        pending: "approval",
      });
    }
    return [...accepted, ...waiting.values()];
  }, [collectives.data, transcript.data, memberCounts]);

  const suggestions = useMemo<Suggestion[]>(() => {
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
  }, [groups.data, items, query]);

  const busy = share.isPending || unshare.isPending;

  function add(id: string) {
    if (busy) return;
    const name = groups.data?.find((g) => g.id === id)?.name ?? "that collective";
    const next: Change = { kind: "add", id, name };
    setChange(next);
    setFailed(null);
    setQuery("");
    share.mutate({ transcriptId, groupId: id }, { onError: () => setFailed(next) });
  }

  function remove(id: string) {
    if (busy) return;
    const name = items.find((item) => item.id === id)?.name ?? "that collective";
    const next: Change = { kind: "remove", id, name };
    setChange(next);
    setFailed(null);
    unshare.mutate({ transcriptId, groupId: id }, { onError: () => setFailed(next) });
  }

  // What the last change did, read from the lists the server re-served after
  // it settled. A collective that does not take this owner's submission is
  // skipped by the server without an error, so an add that left no row behind
  // is said out loud instead of looking like nothing happened.
  let status: string | null = null;
  if (change && busy) {
    status = change.kind === "add" ? `adding ${change.name}…` : `removing ${change.name}…`;
  } else if (change && !failed && change.kind === "add" && !items.some((item) => item.id === change.id)) {
    status = `${change.name} did not take it, so nothing changed.`;
  }

  const visibility = transcript.data?.transcript.visibility;
  const readFailed = collectives.isError;

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="manage access"
      labelId="manage-access-title"
      className="pub-dialog"
      footer={
        <button type="button" className="btn btn-primary btn-sm" onClick={onClose}>
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
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => void collectives.refetch()}>
              retry
            </button>
          </p>
        ) : collectives.isLoading ? (
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
        <CollectivePicker
          suggestions={suggestions}
          query={query}
          onQueryChange={setQuery}
          onAdd={add}
          empty="none of your collectives matches that name."
        />
        <p className="pub-hint">removing a collective takes the transcript back from it: its members lose access.</p>
        {/* Always mounted, so a screen reader hears each change as it lands. */}
        <p className={status ? "pub-line" : "sr-only"} role="status" aria-live="polite">
          {status}
        </p>
        {failed && (
          <p className="pub-line pub-line-alert" role="alert">
            {failed.kind === "add"
              ? `could not add ${failed.name}. nothing changed; try again.`
              : `could not remove ${failed.name}. it can still read it; try again.`}
          </p>
        )}
      </section>
    </Dialog>
  );
}
