/* The one builder of positioned-read bodies for the transcript page's tests and mocks.

   It answers the five positioned reads (header, directory, window, segment, locate) and the
   whole-document `/content` read for one transcript, all from the same turns. Every body is built
   once, at construction, and must pass Schema's z* parsers and the shared directory-position rule;
   the first refusal throws, so a mock built on a body a real client would discard never starts.
   Window and numbering semantics are proved against the real handler in the backend; these bodies
   only have to be ones a real server could send. */

import {
  isConversationTurn,
  isPositionedDirectoryPositionsValid,
  TranscriptReadTransportKind,
  zPositionedDirectory,
  zPositionedSegment,
  zPositionedWindow,
  zTranscriptContentHeader,
  zTranscriptEntryMatch,
} from "@peasant-labs/schema";

const START = Date.parse("2026-11-12T09:00:00Z");

/**
 * @typedef {object} PositionedBodiesSpec
 * @property {string} transcriptId  the village transcript id every context names
 * @property {Array<{ role: string, content: string, sourceEntryRef?: string }>} turns  the main partition, in order
 * @property {number} rowsPerSegment
 * @property {number} [firstRecordedIndex]  the first row's recorded index; rows count up from it (default 0)
 * @property {number} [directoryPerPage]  refs per directory page; omitted serves one page
 * @property {string} [revision]
 * @property {string} [representation]
 */

/** Parse with the real parser or refuse, naming the body and what to fix. */
function mustParse(parser, body, name) {
  const parsed = parser.safeParse(body);
  if (!parsed.success) {
    const issues = parsed.error.issues.map((issue) => `${issue.path.join(".")}: ${issue.message}`).join("; ");
    throw new TypeError(
      `The positioned mock refused to start: its ${name} body is not one Schema's parser accepts (${issues}). ` +
        "Fix the spec passed to buildPositionedBodies (scripts/journey/lib/positioned-bodies.mjs) so every body is one a real server could send.",
    );
  }
  return parsed.data;
}

/**
 * Build every body for one transcript.
 * @param {PositionedBodiesSpec} spec
 * @param {{ mutateBody?: (name: string, body: object) => object }} [options]  a test's edit to one body before it is checked
 */
export function buildPositionedBodies(spec, { mutateBody } = {}) {
  const revision = spec.revision ?? "3";
  const representation = spec.representation ?? "r1";
  const check = (parser, body, name) => mustParse(parser, mutateBody ? mutateBody(name, body) : body, name);
  const transport = { kind: TranscriptReadTransportKind.VillageTranscript, transcriptId: spec.transcriptId };

  const first = spec.firstRecordedIndex ?? 0;
  const turns = spec.turns.map((turn, index) => ({
    index: first + index,
    role: turn.role,
    depth: 0,
    entryType: "text",
    content: turn.content,
    timestamp: new Date(START + index * 47_000).toISOString(),
    ...(turn.sourceEntryRef ? { sourceEntryRef: turn.sourceEntryRef } : {}),
  }));
  const rows = turns.map((turn, position) => ({
    position,
    recordedIndex: turn.index,
    recordedIndexState: "present",
    displayNumber: String(position + 1),
    turn,
  }));
  const chunks = [];
  for (let start = 0; start < rows.length; start += spec.rowsPerSegment) chunks.push(rows.slice(start, start + spec.rowsPerSegment));
  let before = 0;
  const refs = chunks.map((chunk, index) => {
    const count = chunk.filter((row) => isConversationTurn(row.turn)).length;
    const ref = {
      index,
      byteLength: JSON.stringify(chunk).length,
      sourceFirstPosition: chunk[0].position,
      sourceLastPosition: chunk[chunk.length - 1].position,
      conversationTurns: { before, count },
    };
    before += count;
    return ref;
  });
  const context = {
    protocolVersion: "0.2.0",
    representation,
    revision,
    harness: "claude-code",
    model: "claude-opus-4-5",
    nativePresence: "absent",
    validation: "published_strict",
    transport,
    totalTurns: rows.length,
    totalSegments: chunks.length,
    totalConversationTurns: before,
  };
  const session = {
    id: `session-${spec.transcriptId}`,
    project: "positioned-reader",
    harness: "claude-code",
    model: "claude-opus-4-5",
    startTime: new Date(START).toISOString(),
    endTime: new Date(START + rows.length * 47_000).toISOString(),
    durationMins: Math.round((rows.length * 47) / 60),
    turnCount: rows.length,
    toolCallCount: 0,
  };

  const header = check(zTranscriptContentHeader, {
    contractVersion: "0.58.1",
    kind: "session_detail",
    revision,
    representation,
    canonical: true,
    omittedImages: 0,
    sessionHeader: { ...session, conversationTurnCount: before },
  }, "header");

  const perPage = spec.directoryPerPage ?? refs.length;
  const pages = Math.max(1, Math.ceil(refs.length / perPage));
  const directoryPages = Array.from({ length: pages }, (_, page) => {
    const body = check(zPositionedDirectory, {
      context,
      partition: { kind: "main" },
      segments: refs.slice(page * perPage, (page + 1) * perPage),
      usagePresent: "empty",
      ...(spec.directoryPerPage ? { directoryPage: page + 1, directoryPerPage: perPage } : {}),
    }, `directory page ${page + 1}`);
    if (!isPositionedDirectoryPositionsValid(body)) {
      throw new TypeError(
        `The positioned mock refused to start: directory page ${page + 1} breaks the shared conversation-position rule. ` +
          "Fix the turns passed to buildPositionedBodies so each page's spans meet.",
      );
    }
    return body;
  });

  const segments = chunks.map((chunk, index) => check(zPositionedSegment, {
    index,
    rows: chunk,
    byteLength: refs[index].byteLength,
    hasOlder: index > 0,
    hasNewer: index < chunks.length - 1,
    partition: { kind: "main" },
    context,
  }, `segment ${index}`));

  /** A window anchored at a recorded index, or the default open at the first row. A recorded index
      the transcript does not serve snaps to the first row and names no requested position. */
  const windowBody = (turn) => {
    const requested = turn === undefined ? undefined : rows.find((row) => row.recordedIndex === turn);
    const anchor = requested ?? rows[0];
    const anchorSegment = Math.floor(anchor.position / spec.rowsPerSegment);
    return check(zPositionedWindow, {
      context,
      partition: { kind: "main" },
      anchorPosition: anchor.position,
      anchorSegment,
      anchorTurn: anchor.recordedIndex,
      ...(turn === undefined ? {} : { requestedAnchor: turn, ...(requested ? { requestedPosition: requested.position } : {}) }),
      firstPosition: chunks[anchorSegment][0].position,
      lastPosition: chunks[anchorSegment].at(-1).position,
      segments: [anchorSegment],
      span: 1,
      hasOlder: anchorSegment > 0,
      hasNewer: anchorSegment < chunks.length - 1,
    }, `window ${turn ?? "default"}`);
  };

  /** The locator's answer for a carried entry reference, or undefined when no row carries it. */
  const locate = (reference) => {
    const row = rows.find((item) => item.turn.sourceEntryRef === reference);
    if (!row) return undefined;
    return check(zTranscriptEntryMatch, {
      context,
      kind: "source",
      position: row.position,
      recordedIndex: row.recordedIndex,
      segmentOrdinal: Math.floor(row.position / spec.rowsPerSegment),
    }, `locate ${reference}`);
  };

  /** The whole stored document the `/content` route serves, from the same turns. */
  const content = {
    ...session,
    totalTokens: 0,
    tokensIn: 0,
    tokensOut: 0,
    turns,
  };

  return {
    transcriptId: spec.transcriptId,
    context,
    header,
    pages,
    directory: (page = 1) => directoryPages[page - 1],
    window: windowBody,
    segment: (index) => segments[index],
    locate,
    content,
  };
}
