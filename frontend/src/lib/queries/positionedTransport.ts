import type { PositionedHostResponse, PositionedReadTransport, PositionedTarget } from "@peasant-labs/fairtrade/ui";
import {
  TranscriptReadTransportKind,
  zTranscriptSegmentPartition,
  zTranscriptEntryLocateRequest,
} from "@peasant-labs/schema";
import { API_URL_BASE, getAuthHeaders } from "../api";
import { readCarriedRelationshipAnchor } from "../relationshipAnchor";

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const SHORT_DAY = "(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun)";
const LONG_DAY = "(?:Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday)";
const MONTH = `(${MONTHS.join("|")})`;
const TIME = "(\\d{2}):(\\d{2}):(\\d{2})";
// The three HTTP-date forms (RFC 9110 section 5.6.7): IMF-fixdate, and the
// obsolete RFC 850 and asctime forms a recipient must still accept. All are GMT.
const IMF_FIXDATE = new RegExp(`^${SHORT_DAY}, (\\d{2}) ${MONTH} (\\d{4}) ${TIME} GMT$`);
const RFC850_DATE = new RegExp(`^${LONG_DAY}, (\\d{2})-${MONTH}-(\\d{2}) ${TIME} GMT$`);
const ASCTIME_DATE = new RegExp(`^${SHORT_DAY} ${MONTH} ( \\d|\\d{2}) ${TIME} (\\d{4})$`);

/** Milliseconds since the epoch for a valid HTTP-date, or undefined. */
function httpDate(value: string, now: number): number | undefined {
  let day: string, month: string, year: number, hour: string, minute: string, second: string;
  let match = IMF_FIXDATE.exec(value);
  if (match) {
    [, day, month, , hour, minute, second] = match;
    year = Number(match[3]);
  } else if ((match = RFC850_DATE.exec(value))) {
    [, day, month, , hour, minute, second] = match;
    // A two-digit year more than 50 years ahead is the most recent past year
    // with the same last two digits.
    const current = new Date(now).getUTCFullYear();
    year = Math.floor(current / 100) * 100 + Number(match[3]);
    if (year > current + 50) year -= 100;
  } else if ((match = ASCTIME_DATE.exec(value))) {
    [, month, day, hour, minute, second] = match;
    year = Number(match[6]);
  } else {
    return undefined;
  }
  const monthIndex = MONTHS.indexOf(month);
  const time = Date.UTC(year, monthIndex, Number(day), Number(hour), Number(minute), Number(second));
  const date = new Date(time);
  // Date.UTC rolls an impossible date (31 Feb, 25:00) into the next one; refuse it.
  if (date.getUTCDate() !== Number(day) || date.getUTCMonth() !== monthIndex || date.getUTCHours() !== Number(hour)
    || date.getUTCMinutes() !== Number(minute) || date.getUTCSeconds() !== Number(second)) {
    return undefined;
  }
  return time;
}

/**
 * A `Retry-After` value in seconds: delta-seconds, or an HTTP-date measured
 * against the injected clock (a date already past is no wait). Anything else,
 * including a signed or fractional number, is undefined, which the reader
 * treats as its own default wait.
 */
export function retryAfterSeconds(value: string | null, now: () => number): number | undefined {
  if (value === null) return undefined;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) {
    const seconds = Number(trimmed);
    return Number.isSafeInteger(seconds) ? seconds : undefined;
  }
  const at = now();
  const date = httpDate(trimmed, at);
  return date === undefined ? undefined : Math.max(0, Math.ceil((date - at) / 1000));
}

type RequestFields = {
  partition?: unknown;
  index?: unknown;
  page?: unknown;
  turn?: unknown;
  revision?: unknown;
  representation?: unknown;
};

/** HTTP only: response validation and status interpretation belong to Fairtrade. */
export function villagePositionedTransport(
  transcriptId: string,
  now: () => number = Date.now,
): PositionedReadTransport {
  const base = `${API_URL_BASE}/transcripts/${encodeURIComponent(transcriptId)}`;

  async function request(path: string, signal: AbortSignal, query: URLSearchParams, body?: unknown): Promise<PositionedHostResponse> {
    const response = await fetch(`${base}${path}?${query}`, {
      method: body === undefined ? "GET" : "POST",
      headers: { ...getAuthHeaders(), ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
      cache: "no-store",
      signal,
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const value: unknown = await response.json().catch(() => undefined);
    const seconds = retryAfterSeconds(response.headers.get("Retry-After"), now);
    return { status: response.status, body: value, ...(seconds === undefined ? {} : { retryAfterSeconds: seconds }) };
  }

  function params(raw: object): { fields: RequestFields; query: URLSearchParams } {
    const fields: RequestFields = raw;
    const query = new URLSearchParams({ v: "2" });
    if (fields.partition !== undefined) {
      const partition = zTranscriptSegmentPartition.parse(fields.partition);
      query.set("partition", partition.kind);
      if (partition.sectionIndex != null) query.set("section", String(partition.sectionIndex));
    }
    for (const key of ["revision", "representation"] as const) {
      if (typeof fields[key] === "string") query.set(key, fields[key]);
    }
    return { fields, query };
  }

  return {
    header: (raw, signal) => {
      const { query } = params(raw);
      return request("/header", signal, query);
    },
    directory: (raw, signal) => {
      const { fields, query } = params(raw);
      if (typeof fields.page === "number") query.set("page", String(fields.page));
      return request("/segments", signal, query);
    },
    segment: (raw, signal) => {
      const { fields, query } = params(raw);
      if (typeof fields.index !== "number" || !Number.isSafeInteger(fields.index) || fields.index < 0) {
        throw new TypeError("Cannot read a transcript segment: its index must be a nonnegative integer.");
      }
      return request(`/segments/${fields.index}`, signal, query);
    },
    window: (raw, signal) => {
      const { fields, query } = params(raw);
      if (typeof fields.turn === "number") query.set("anchor", String(fields.turn));
      return request("/window", signal, query);
    },
    locate: (raw, signal) => {
      const { query } = params(raw);
      return request("/segments/locate", signal, query, zTranscriptEntryLocateRequest.parse(raw));
    },
  };
}

/**
 * What a positioned read opens at, from the page's mount query: the read
 * identity every response must carry, a `?turn=N` deep link's recorded index,
 * and a carried branch point the locator resolves. The carried point is used
 * only while it still names this transcript's current revision.
 */
export function positionedReadTarget(
  transcriptId: string,
  search: string,
  currentContentHash: string | null | undefined,
): PositionedTarget {
  const raw = new URLSearchParams(search).get("turn");
  const turn = raw != null && /^\d+$/.test(raw) ? Number(raw) : undefined;
  const anchor = readCarriedRelationshipAnchor(search, currentContentHash);
  return {
    transport: { kind: TranscriptReadTransportKind.VillageTranscript, transcriptId },
    ...(turn !== undefined && Number.isSafeInteger(turn) ? { turn } : {}),
    ...(anchor ? { entry: { reference: anchor.sourceEntryRef, kind: anchor.kind } } : {}),
  };
}
