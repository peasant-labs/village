/**
 * Where a transcript was opened from: home, one of the person's collectives,
 * or a pull request. The transcript page reads it from its address (`?in=`)
 * so the sidebar marks the place the person came from and the breadcrumb
 * leads back there, and Back and Forward keep it because it is part of the
 * address.
 *
 * A link from a list carries its own context. A link with none (shared, typed,
 * or from a page that names no place) is given one by the transcript page from
 * what the server already answered: the one collective of the person's that
 * holds the transcript, or home.
 */
export type TranscriptContext =
  | { kind: "home" }
  | { kind: "collective"; id: string }
  | { kind: "pull"; owner: string; name: string; number: number };

/** The query parameter that carries the context. */
export const CONTEXT_PARAM = "in";

const ID = /^[A-Za-z0-9-]{1,64}$/;
const SEGMENT = /^[A-Za-z0-9._-]{1,100}$/;

/** The context an address names, or null when it names none (or names it wrongly). */
export function parseTranscriptContext(raw: string | null | undefined): TranscriptContext | null {
  if (!raw) return null;
  if (raw === "home") return { kind: "home" };
  if (raw.startsWith("collective:")) {
    const id = raw.slice("collective:".length);
    return ID.test(id) ? { kind: "collective", id } : null;
  }
  if (raw.startsWith("pull:")) {
    const [owner, name, number, ...rest] = raw.slice("pull:".length).split("/");
    const n = Number(number);
    if (rest.length || !SEGMENT.test(owner ?? "") || !SEGMENT.test(name ?? "") || !Number.isInteger(n) || n < 1) return null;
    return { kind: "pull", owner, name, number: n };
  }
  return null;
}

/** The context as its address value: `home`, `collective:<id>` or `pull:<owner>/<name>/<number>`. */
export function formatTranscriptContext(context: TranscriptContext): string {
  switch (context.kind) {
    case "home":
      return "home";
    case "collective":
      return `collective:${context.id}`;
    case "pull":
      return `pull:${context.owner}/${context.name}/${context.number}`;
  }
}

/**
 * A query string with the context set, after every other parameter it already
 * has. The value keeps its `:` and `/` readable, which a query allows.
 */
export function withTranscriptContext(search: string | URLSearchParams, context: TranscriptContext): string {
  const params = new URLSearchParams(search);
  params.delete(CONTEXT_PARAM);
  const value = encodeURIComponent(formatTranscriptContext(context)).replace(/%3A/gi, ":").replace(/%2F/gi, "/");
  const rest = params.toString();
  return `${rest ? `${rest}&` : ""}${CONTEXT_PARAM}=${value}`;
}

/** A transcript's address, carrying where it was opened from when that is known. */
export function transcriptHref(transcriptId: string, context?: TranscriptContext | null, turn?: number): string {
  const params = new URLSearchParams();
  if (turn != null) params.set("turn", String(turn));
  const query = context ? withTranscriptContext(params, context) : params.toString();
  return `/transcripts/${transcriptId}${query ? `?${query}` : ""}`;
}

/**
 * The context for a transcript opened with none: the one collective of the
 * person's that holds it (an approved contribution), or home when it is in
 * none of theirs or in more than one.
 */
export function inferTranscriptContext(
  shares: readonly { group_id: string; status?: string | null }[],
  myCollectiveIds: ReadonlySet<string>,
): TranscriptContext {
  const mine = [...new Set(shares.filter((s) => (s.status ?? "approved") === "approved" && myCollectiveIds.has(s.group_id)).map((s) => s.group_id))];
  return mine.length === 1 ? { kind: "collective", id: mine[0] } : { kind: "home" };
}
