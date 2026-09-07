package handler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/peasant-labs/schema"
)

// scanStoredContent runs before shape sniffing or migration can discard lexical
// evidence. Legacy JSONL keeps its line-oriented decoding and ordinary text is
// not subject to native-metadata budgets.
func scanStoredContent(raw []byte) error {
	policy := schema.RawJSONPathPolicy{
		MaxDocumentBytes:       8 << 20,
		MaxDocumentDepth:       64,
		OpaqueMetadataPointers: []string{"/nativeMetadata/*/data", "/sessionDetail/nativeMetadata/*/data"},
	}
	if len(raw) > policy.MaxDocumentBytes || json.Valid(raw) {
		return schema.ScanRawJSONDocument(raw, policy)
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := schema.ScanRawJSONDocument(line, policy); err != nil {
			return fmt.Errorf("transcript raw validation failed in handler.scanStoredContent before decoding, migration, serving or storage; no response or replacement was produced; repair the JSON or legacy JSONL and retry: %w", err)
		}
	}
	return nil
}
