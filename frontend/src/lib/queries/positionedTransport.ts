import type { PositionedHostResponse, PositionedReadTransport } from "@peasant-labs/fairtrade/ui";
import { zTranscriptSegmentPartition, zTranscriptEntryLocateRequest } from "@peasant-labs/schema";
import { API_URL_BASE, getAuthHeaders } from "../api";

function retryAfter(value: string | null, now: () => number): number | undefined {
  if (value === null) return undefined;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) return Number(trimmed);
  const date = Date.parse(trimmed);
  return Number.isFinite(date) ? Math.max(0, (date - now()) / 1000) : undefined;
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
    const seconds = retryAfter(response.headers.get("Retry-After"), now);
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
