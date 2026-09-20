package handler

import (
	"bytes"
	"encoding/json"
)

// The database stores the validated relationship array, not read navigation.
// Omit an empty legacy array to retain the old response shape.
func graphRelationshipsResponse(raw []byte) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
		return nil
	}
	return bytes.Clone(raw)
}
