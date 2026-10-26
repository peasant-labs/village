/**
 * The `parent_transcript_id` the server would send for each row of one
 * response, for fixtures that state their rows by owner and harness session id.
 *
 * The server resolves a row's `parent_session_id` to the transcript published
 * from that session WITHIN THE SAME OWNER. A fixture models that here, over the
 * rows it serves: a parent that the response does not carry resolves to null,
 * which the fold treats exactly like a parent on another page.
 */
export function resolvedParentTranscriptIDs(
  rows: readonly { id: string; ownerID: string; localID: string; parentSessionID: string | null }[],
): Map<string, string | null> {
  const bySession = new Map<string, string>();
  for (const row of rows) {
    const key = `${row.ownerID}\u0000${row.localID}`;
    if (!bySession.has(key)) bySession.set(key, row.id);
  }
  return new Map(
    rows.map((row) => [
      row.id,
      row.parentSessionID === null || row.parentSessionID.trim() === ""
        ? null
        : (bySession.get(`${row.ownerID}\u0000${row.parentSessionID}`) ?? null),
    ]),
  );
}

/**
 * The same resolution for one owner's transcripts, applied in place of each
 * transcript's `parent_transcript_id`: a home, profile or project response
 * holds one person's rows, so every row shares the owner.
 */
export function withResolvedParents<
  T extends { id: string; local_id: string; parent_session_id: string | null; parent_transcript_id: string | null },
>(transcripts: readonly T[]): T[] {
  const parents = resolvedParentTranscriptIDs(
    transcripts.map((t) => ({ id: t.id, ownerID: "one-owner", localID: t.local_id, parentSessionID: t.parent_session_id })),
  );
  return transcripts.map((t) => ({ ...t, parent_transcript_id: parents.get(t.id) ?? null }));
}
