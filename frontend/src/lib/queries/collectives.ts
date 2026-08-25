import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { ContributedCollective, ShareEvent, TranscriptCollective } from "../types";

/**
 * The collectives the SIGNED-IN caller has offered transcripts to, with the
 * three counters their own profile renders.
 *
 * `GET /users/me/collectives/contributions` is authenticated and has no
 * username variant by design, so there is no way to ask for anybody else's
 * contributions. `enabled` exists so a profile that is not the caller's own
 * never issues the request at all: a section that is absent for other people
 * must also be silent on the network, or the request itself becomes the leak.
 */
export function useMyCollectiveContributions(enabled: boolean) {
  return useQuery({
    queryKey: ["my-collective-contributions"],
    queryFn: async () => {
      const res = await api<{ collectives: ContributedCollective[] }>(
        "/users/me/collectives/contributions",
      );
      return res.collectives ?? [];
    },
    enabled,
  });
}

/**
 * The collectives holding this transcript that the VIEWER may see.
 *
 * The server applies both the collective-visibility rule and the transcript
 * owner's contributor opt-in inside the query, and answers with an empty list
 * rather than a refusal when either withholds everything. An empty result is
 * therefore indistinguishable from "this transcript is in no collective", by
 * design, and callers must keep it that way.
 */
export function useTranscriptCollectives(transcriptId: string) {
  return useQuery({
    queryKey: ["transcript-collectives", transcriptId],
    queryFn: async () => {
      const res = await api<{ collectives: TranscriptCollective[] }>(
        `/transcripts/${encodeURIComponent(transcriptId)}/collectives`,
      );
      return res.collectives ?? [];
    },
    enabled: !!transcriptId,
  });
}

/**
 * The full share-event history for one (transcript, collective) pair, oldest
 * event first, as an audit log of every state change.
 *
 * Owner-only by ROUTE: the path names no user, so no request through it can
 * ask about anyone but the caller, and a caller who does not own the
 * transcript is answered 404 rather than 403. `enabled` keeps the request
 * behind an explicit user action, so opening a profile does not fetch a
 * history per contributed collective.
 */
export function useShareEventHistory(
  groupId: string,
  transcriptId: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["share-event-history", groupId, transcriptId],
    queryFn: () =>
      api<ShareEvent[]>(
        `/users/me/collectives/${encodeURIComponent(groupId)}/transcripts/${encodeURIComponent(transcriptId)}/events`,
      ),
    enabled: enabled && !!groupId && !!transcriptId,
  });
}
