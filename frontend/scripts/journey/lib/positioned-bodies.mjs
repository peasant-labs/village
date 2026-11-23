/* The one builder of positioned-read bodies for the transcript page's tests and mocks.

   It answers the five positioned reads (header, directory, window, segment, locate) and the
   whole-document `/content` read for one transcript, all from the same turns. Every body is built
   once, at construction, and must pass Schema's z* parsers and the shared directory-position rule;
   the first refusal throws, so a mock built on a body a real client would discard never starts.
   The `/content` body passes Schema's published session-detail parser the same way. The bodies'
   `requestRefusal` answers a request a real server would refuse (no `v=2`, a missing or stale
   revision and representation echo, an invalid locate body), so a page that stops sending them
   fails against the mocks too.
   Window and numbering semantics are proved against the real handler in the backend; these bodies
   only have to be ones a real server could send. */

import {
  EarlierHistoryState,
  isConversationTurn,
  isPositionedDirectoryPositionsValid,
  parseSessionDetailPayloadValue,
  TranscriptReadTransportKind,
  TranscriptRecordedIndexState,
  TranscriptSegmentPartitionKind,
  zPositionedDirectory,
  zPositionedSegment,
  zPositionedWindow,
  zTranscriptContentHeader,
  zTranscriptEntryLocateRequest,
  zTranscriptEntryMatch,
} from "@peasant-labs/schema";

/** The `v` selector every positioned read carries: the positioned protocol's generation. */
const POSITIONED_READ_SELECTOR = "2";

/** The positioned routes a request names, for `requestRefusal`. Closed. */
export const PositionedRoute = Object.freeze({
  Header: "header",
  Directory: "directory",
  Window: "window",
  Segment: "segment",
  Locate: "locate",
});

const START = Date.parse("2026-11-12T09:00:00Z");

/**
 * @typedef {{ role: string, content: string, sourceEntryRef?: string }} PositionedTurnSpec
 * @typedef {object} PositionedBodiesSpec
 * @property {string} transcriptId  the village transcript id every context names
 * @property {PositionedTurnSpec[]} turns  the main partition, in order
 * @property {number} rowsPerSegment
 * @property {number} [firstRecordedIndex]  the first main row's recorded index; rows count up from it (default 0)
 * @property {number[]} [numberAbsent]  main-partition positions the server sends no display number for
 * @property {number[]} [recordedIndexAbsent]  main-partition positions the server serves with recordedIndexState "absent" and no recordedIndex
 * @property {number} [directoryPerPage]  main refs per directory page; omitted serves one page
 * @property {Array<{ turns: PositionedTurnSpec[], state?: string }>} [history]  earlier-history sections, in order
 * @property {{ project?: string, gitBranch?: string, gitRemote?: string }} [session]  the session fields the
 *   server's projection keeps; omitted fields are withheld from the header and the stored copy alike
 * @property {string} [revision]
 * @property {string} [representation]
 */

/** The refusal for a body Schema's parser refuses: which body, why, and what to fix. */
function refusedBody(name, issues) {
  return new TypeError(
    `The positioned mock refused its ${name} body: it is not one Schema's parser accepts (${issues}). ` +
      "Fix the spec passed to buildPositionedBodies (scripts/journey/lib/positioned-bodies.mjs) so every body is one a real server could send.",
  );
}

/** Parse with the real parser or refuse, naming the body and what to fix. */
function mustParse(parser, body, name) {
  const parsed = parser.safeParse(body);
  if (!parsed.success) {
    throw refusedBody(name, parsed.error.issues.map((issue) => `${issue.path.join(".")}: ${issue.message}`).join("; "));
  }
  return parsed.data;
}

/** The partition key a request names: `main`, or `history:<section>`. */
export function partitionKey(partition) {
  return partition?.kind === TranscriptSegmentPartitionKind.History ? `${TranscriptSegmentPartitionKind.History}:${partition.sectionIndex}` : TranscriptSegmentPartitionKind.Main;
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
  const absent = new Set(spec.numberAbsent ?? []);
  const recordedAbsent = new Set(spec.recordedIndexAbsent ?? []);
  const sessionFields = spec.session ?? { project: "positioned-reader" };

  /** One partition's rows, segments, refs and context. History sections record their own indexes from 0. */
  function partitionOf(turnSpecs, partition, firstIndex, name) {
    const turns = turnSpecs.map((turn, index) => ({
      index: firstIndex + index,
      role: turn.role,
      depth: 0,
      entryType: "text",
      content: turn.content,
      timestamp: new Date(START + index * 47_000).toISOString(),
      ...(turn.sourceEntryRef ? { sourceEntryRef: turn.sourceEntryRef } : {}),
    }));
    // The display numbers are this mock's own rule (position + 1), not the server's numbering,
    // which the backend proves against the real handler. Production code never derives a number:
    // it shows the label each row is served with.
    const rows = turns.map((turn, position) => {
      const noRecordedIndex = partition.kind === TranscriptSegmentPartitionKind.Main && recordedAbsent.has(position);
      return {
        position,
        ...(noRecordedIndex
          ? { recordedIndexState: TranscriptRecordedIndexState.Absent }
          : { recordedIndex: turn.index, recordedIndexState: TranscriptRecordedIndexState.Present }),
        ...(partition.kind === TranscriptSegmentPartitionKind.Main && absent.has(position) ? {} : { displayNumber: String(position + 1) }),
        turn,
      };
    });
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
    const segments = chunks.map((chunk, index) => check(zPositionedSegment, {
      index,
      rows: chunk,
      byteLength: refs[index].byteLength,
      hasOlder: index > 0,
      hasNewer: index < chunks.length - 1,
      partition,
      context,
    }, name === TranscriptSegmentPartitionKind.Main ? `segment ${index}` : `${name} segment ${index}`));
    return { partition, turns, rows, chunks, refs, context, segments };
  }

  const main = partitionOf(spec.turns, { kind: TranscriptSegmentPartitionKind.Main }, spec.firstRecordedIndex ?? 0, TranscriptSegmentPartitionKind.Main);
  // Every section has a state: the stored copy requires one. A spec that names none gets the
  // unresolved state, in the directory and the stored copy alike.
  const history = (spec.history ?? []).map((section, sectionIndex) => ({
    ...partitionOf(section.turns, { kind: TranscriptSegmentPartitionKind.History, sectionIndex }, 0, `history ${sectionIndex}`),
    state: section.state ?? EarlierHistoryState.UncertainUnresolved,
  }));

  const session = {
    id: `session-${spec.transcriptId}`,
    ...sessionFields,
    harness: "claude-code",
    model: "claude-opus-4-5",
    startTime: new Date(START).toISOString(),
    endTime: new Date(START + main.rows.length * 47_000).toISOString(),
    durationMins: Math.round((main.rows.length * 47) / 60),
    turnCount: main.rows.length,
    toolCallCount: 0,
  };

  const header = check(zTranscriptContentHeader, {
    contractVersion: "0.58.1",
    kind: "session_detail",
    revision,
    representation,
    canonical: true,
    omittedImages: 0,
    sessionHeader: { ...session, conversationTurnCount: main.context.totalConversationTurns },
  }, "header");

  /** Every directory page of one partition, each checked against the parser and the position rule. */
  function directoryPages(part, name) {
    const perPage = part.partition.kind === TranscriptSegmentPartitionKind.Main && spec.directoryPerPage ? spec.directoryPerPage : part.refs.length;
    const pages = Math.max(1, Math.ceil(part.refs.length / perPage));
    return Array.from({ length: pages }, (_, page) => {
      const label = part.partition.kind === TranscriptSegmentPartitionKind.Main ? `directory page ${page + 1}` : `${name} directory page ${page + 1}`;
      const body = check(zPositionedDirectory, {
        context: part.context,
        partition: part.partition,
        segments: part.refs.slice(page * perPage, (page + 1) * perPage),
        usagePresent: part.partition.kind === TranscriptSegmentPartitionKind.Main ? "empty" : "unknown",
        ...(part.partition.kind === TranscriptSegmentPartitionKind.Main && history.length > 0
          ? {
              history: history.map((section, index) => ({
                index,
                state: section.state,
                totalTurns: section.context.totalTurns,
                totalConversationTurns: section.context.totalConversationTurns,
                usagePresent: "unknown",
              })),
            }
          : {}),
        ...(perPage < part.refs.length ? { directoryPage: page + 1, directoryPerPage: perPage } : {}),
      }, label);
      if (!isPositionedDirectoryPositionsValid(body)) {
        throw new TypeError(
          `The positioned mock refused to start: ${label} breaks the shared conversation-position rule. ` +
            "Fix the turns passed to buildPositionedBodies so each page's spans meet.",
        );
      }
      return body;
    });
  }

  /** A window anchored at a recorded index, or the default open at the first row. A recorded index
      the transcript does not serve snaps to the first row and names no requested position. */
  function windowBody(part, turn, name) {
    const requested = turn === undefined ? undefined : part.rows.find((row) => row.recordedIndex === turn);
    const anchor = requested ?? part.rows[0];
    const anchorSegment = Math.floor(anchor.position / spec.rowsPerSegment);
    return check(zPositionedWindow, {
      context: part.context,
      partition: part.partition,
      anchorPosition: anchor.position,
      anchorSegment,
      ...(anchor.recordedIndex !== undefined ? { anchorTurn: anchor.recordedIndex } : {}),
      ...(turn === undefined ? {} : { requestedAnchor: turn, ...(requested ? { requestedPosition: requested.position } : {}) }),
      firstPosition: part.chunks[anchorSegment][0].position,
      lastPosition: part.chunks[anchorSegment].at(-1).position,
      segments: [anchorSegment],
      span: 1,
      hasOlder: anchorSegment > 0,
      hasNewer: anchorSegment < part.chunks.length - 1,
    }, name);
  }

  const partitions = new Map([[partitionKey(main.partition), main], ...history.map((part) => [partitionKey(part.partition), part])]);
  const directories = new Map([...partitions].map(([key, part]) => [key, directoryPages(part, key)]));
  const defaultWindows = new Map([...partitions].map(([key, part]) => [key, windowBody(part, undefined, key === TranscriptSegmentPartitionKind.Main ? "window default" : `${key} window default`)]));

  /** The locator's answer for a carried entry reference, or undefined when no main row carries it. */
  const locate = (reference) => {
    const row = main.rows.find((item) => item.turn.sourceEntryRef === reference);
    if (!row) return undefined;
    return check(zTranscriptEntryMatch, {
      context: main.context,
      kind: "source",
      position: row.position,
      recordedIndex: row.recordedIndex,
      segmentOrdinal: Math.floor(row.position / spec.rowsPerSegment),
    }, `locate ${reference}`);
  };

  /** The whole stored document the `/content` route serves, from the same turns. It must pass
      Schema's session-detail parser, and is served as built. */
  const built = {
    ...session,
    totalTokens: 0,
    tokensIn: 0,
    tokensOut: 0,
    turns: main.turns,
    ...(history.length > 0 ? { earlierHistory: history.map((section) => ({ state: section.state, turns: section.turns })) } : {}),
  };
  const content = mutateBody ? mutateBody("content", built) : built;
  try {
    parseSessionDetailPayloadValue(content);
  } catch (error) {
    throw refusedBody("content", error instanceof Error ? error.message : String(error));
  }

  /** A refusal a real server would send for this request, or undefined when it is one the server
      answers. `query` is the request's URLSearchParams; `body` the parsed locate body. */
  function requestRefusal(route, query, body) {
    const where = `the positioned mock refused a ${route} read`;
    const fix = "fix the request the page makes in src/lib/queries/positionedTransport.ts";
    if (query.get("v") !== POSITIONED_READ_SELECTOR) {
      return { status: 400, error: `${where}: it carries no v=${POSITIONED_READ_SELECTOR}, so a real server would read it as another protocol; ${fix}.` };
    }
    const stale = (revisionValue, representationValue) =>
      revisionValue !== main.context.revision || representationValue !== main.context.representation;
    if (route === PositionedRoute.Segment || route === PositionedRoute.Window) {
      const echoed = [query.get("revision"), query.get("representation")];
      // A window may open before the reader knows the revision; a segment never does.
      if (route === PositionedRoute.Window && echoed.every((value) => value === null)) return undefined;
      if (echoed.some((value) => value === null)) {
        return { status: 400, error: `${where}: it echoes only part of the revision and representation, which a real server refuses; ${fix}.` };
      }
      if (stale(...echoed)) {
        return { status: 409, error: `${where}: its revision or representation echo names another copy than the one served; the reader must reload.` };
      }
    }
    if (route === PositionedRoute.Locate) {
      const parsed = zTranscriptEntryLocateRequest.safeParse(body);
      if (!parsed.success) return { status: 400, error: `${where}: its body is not a locate request Schema's parser accepts; ${fix}.` };
      if (stale(parsed.data.revision, parsed.data.representation)) {
        return { status: 409, error: `${where}: its revision or representation names another copy than the one served; the reader must reload.` };
      }
    }
    return undefined;
  }

  return {
    transcriptId: spec.transcriptId,
    context: main.context,
    header,
    pages: directories.get(TranscriptSegmentPartitionKind.Main).length,
    /** The main partition's directory page (1-based). */
    directory: (page = 1) => directories.get(TranscriptSegmentPartitionKind.Main)[page - 1],
    /** The main partition's window: the default open, or anchored at a recorded index. */
    window: (turn) => (turn === undefined ? defaultWindows.get(TranscriptSegmentPartitionKind.Main) : windowBody(main, turn, `window ${turn}`)),
    /** The main partition's segment. */
    segment: (index) => main.segments[index],
    /** Any partition's directory page, window and segment, by the request's partition; undefined when absent. */
    directoryFor: (partition, page = 1) => directories.get(partitionKey(partition))?.[page - 1],
    windowFor: (partition, turn) => {
      const part = partitions.get(partitionKey(partition));
      if (!part) return undefined;
      return turn === undefined ? defaultWindows.get(partitionKey(partition)) : windowBody(part, turn, `${partitionKey(partition)} window ${turn}`);
    },
    segmentFor: (partition, index) => partitions.get(partitionKey(partition))?.segments[index],
    locate,
    content,
    requestRefusal,
  };
}
