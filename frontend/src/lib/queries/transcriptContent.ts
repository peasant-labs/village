import {
  parseSessionDetailPayloadText,
  parseTranscriptContentText,
  scanRawJsonText,
} from "@peasant-labs/schema";

const contentPolicy = {
  maxDocumentBytes: 8 << 20,
  maxDocumentDepth: 64,
  opaqueMetadataPointers: ["/nativeMetadata/*/data", "/sessionDetail/nativeMetadata/*/data"],
};

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** The fetch boundary: syntax evidence must survive until Schema validates it. */
export function parseTranscriptResponseText(text: string): unknown {
  try {
    scanRawJsonText(text, contentPolicy);
  } catch (error) {
    // Only legacy JSONL may contain multiple documents. A failed typed envelope
    // must never be retried through an unvalidated JSON.parse fallback.
    const lines = text.split("\n").filter((line) => line.trim());
    if (lines.length < 2) throw error;
    return lines.map((line) => {
      scanRawJsonText(line, contentPolicy);
      const value: unknown = JSON.parse(line);
      if (isObject(value) && ("contractVersion" in value || "sessionDetail" in value || "turns" in value)) {
        throw new Error("Transcript content validation failed during response parsing: a detail envelope was mixed into legacy JSONL; no transcript was rendered; republish one supported content envelope and retry.");
      }
      return value;
    });
  }
  const value: unknown = JSON.parse(text);
  if (isObject(value)) {
    const detail = "sessionDetail" in value ? value.sessionDetail : value;
    if (isObject(detail) && Array.isArray(detail.turns)) {
      for (const turn of detail.turns) {
        if (!isObject(turn) || !Array.isArray(turn.toolCalls)) continue;
        if (turn.toolCalls.some((tool) => isObject(tool) && "namespace" in tool)) {
          throw new Error("Tool namespace preservation is unavailable during transcript response parsing: the pinned Schema cannot retain this field; nothing was rendered; retry after upgrading to the canonical namespace release.");
        }
      }
    }
    if ("contractVersion" in value || "sessionDetail" in value) return parseTranscriptContentText(text);
    // The content display endpoint serves a bare public detail after migration.
    if ("turns" in value) return parseSessionDetailPayloadText(text);
  }
  return value;
}
