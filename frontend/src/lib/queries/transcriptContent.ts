import {
  parseSessionDetailPayloadText,
  parseTranscriptContentText,
  scanRawJsonText,
  zObservedModelID,
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
export function parseTranscriptResponseText(text: string, knownHarness?: string): unknown {
  try {
    scanRawJsonText(text, contentPolicy);
  } catch (error) {
    // Only legacy JSONL may contain multiple documents. A failed typed envelope
    // must never be retried through an unvalidated JSON.parse fallback.
    // Per-line scanning must not turn the total response-byte cap into a cap
    // per record. Keep this caller limit even for otherwise valid JSONL.
    if (new TextEncoder().encode(text).length > contentPolicy.maxDocumentBytes) throw error;
    const lines = text.split("\n").filter((line) => line.trim());
    if (lines.length < 2) throw error;
    if (knownHarness === "pi") throw error;
    return lines.map((line) => {
      scanRawJsonText(line, contentPolicy);
      const value: unknown = JSON.parse(line);
      if (embeddedPublicRoot(value, false)) {
        throw new Error("Transcript content validation failed during response parsing: a detail envelope was mixed into legacy JSONL; no transcript was rendered; republish one supported content envelope and retry.");
      }
      return value;
    });
  }
  const value: unknown = JSON.parse(text);
  const envelope = isObject(value) && ("sessionDetail" in value || "contractVersion" in value);
  const detail = envelope ? value.sessionDetail : value;
  const piIdentity = isObject(detail) && [detail.harness, detail.provider, detail.modelHarness].includes("pi");
  const evidence = publicEvidence(detail);
  if (evidence.namespace) {
    throw new Error("Tool namespace preservation is unavailable during transcript response parsing: the pinned Schema cannot retain this field; nothing was rendered; retry after upgrading to the canonical namespace release.");
  }
  // Dispatch before invoking a strict parser. There is no strict-error fallback:
  // null/empty new members and trusted Pi context cannot select legacy parsing.
  if (knownHarness === "pi" || piIdentity || evidence.strict) {
    if ((knownHarness === "pi" || piIdentity) && (!isObject(detail) || detail.harness !== "pi")) {
      throw new Error("Pi transcript response disagrees with its recorded harness during content parsing; nothing was rendered; republish a complete Pi session detail and retry.");
    }
    return envelope ? parseTranscriptContentText(text) : parseSessionDetailPayloadText(text);
  }
  if (embeddedPublicRoot(value, isObject(value))) {
    throw new Error("Transcript response contains public detail inside legacy records; nothing was rendered; republish one supported content envelope and retry.");
  }
  if (envelope && (!isObject(detail) || value.kind !== "session_detail")) {
    throw new Error("Legacy transcript envelope has no supported session detail during response parsing; nothing was rendered; republish a session_detail envelope and retry.");
  }
  // Historical reads preserve the existing representation, including Go-zero
  // timestamps, an empty harness and null toolCalls. Only observed model values
  // are validated here; the historical non-assistant allowance remains intact.
  if (isObject(detail) && Array.isArray(detail.turns)) {
    for (const turn of detail.turns) {
      if (isObject(turn) && "observedModel" in turn) zObservedModelID.parse(turn.observedModel);
    }
  }
  return value;
}

function publicEvidence(value: unknown): { strict: boolean; namespace: boolean } {
  let strict = false;
  let namespace = false;
  if (isObject(value)) {
    strict = "nativeMetadata" in value;
    if (Array.isArray(value.turns)) {
      for (const turn of value.turns) {
        if (!isObject(turn)) continue;
        if ("usage" in turn || "sourceEntryRef" in turn) strict = true;
        if (!Array.isArray(turn.toolCalls)) continue;
        for (const tool of turn.toolCalls) {
          if (!isObject(tool)) continue;
          if ("usage" in tool || "callEntryRef" in tool || "resultEntryRef" in tool) strict = true;
          if ("namespace" in tool) namespace = strict = true;
        }
      }
    }
  }
  return { strict, namespace };
}

function publicRootShape(node: Record<string, unknown>): boolean {
  if ("turns" in node && (node.turns === null || Array.isArray(node.turns))) return true;
  if (node.harness === "pi" || node.kind === "session_detail") return true;
  if (isObject(node.sessionDetail)) {
    return typeof node.contractVersion === "string" || "nativeMetadata" in node.sessionDetail || publicRootShape(node.sessionDetail);
  }
  return false;
}

function legacyMessageShape(node: Record<string, unknown>): boolean {
  return ("content" in node || "parts" in node) && typeof node.role === "string";
}

function opaqueProviderChild(node: Record<string, unknown>, key: string, child: unknown): boolean {
  if (legacyMessageShape(node) && ["content", "parts", "toolCalls", "tool_calls"].includes(key)) return true;
  if (key === "message" && isObject(child) && legacyMessageShape(child)) return true;
  return typeof node.type === "string" && ["tool_use", "tool_result", "toolCall", "function_call", "function_call_output"].includes(node.type) && ["input", "arguments", "result", "output", "content"].includes(key);
}

function embeddedPublicRoot(value: unknown, allowRoot: boolean): boolean {
  if (isObject(value)) {
    if (!allowRoot && publicRootShape(value)) return true;
    return Object.entries(value).some(([key, child]) => !opaqueProviderChild(value, key, child) && embeddedPublicRoot(child, allowRoot && key === "sessionDetail"));
  }
  return Array.isArray(value) && value.some((child) => embeddedPublicRoot(child, false));
}
