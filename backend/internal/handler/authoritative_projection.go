package handler

import (
	"encoding/json"

	"github.com/peasant-labs/schema"
)

// projectAuthoritativePublish maps a validated authoritative request onto the
// legacy PublishRequest that persistence reads, and returns the authoritative
// bytes stored beside the content. The JSON copy only carries fields keyed the
// same on both wires, so a field keyed differently is assigned here.
func projectAuthoritativePublish(authoritative schema.AuthoritativePublishRequest) (schema.PublishRequest, []byte, error) {
	stored, err := json.Marshal(authoritative)
	if err != nil {
		return schema.PublishRequest{}, nil, err
	}
	var req schema.PublishRequest
	if err := json.Unmarshal(stored, &req); err != nil {
		return schema.PublishRequest{}, nil, err
	}
	// The authoritative successor field is parentSessionId, while the legacy
	// compatibility projection intentionally retains parentUuid. Materialize
	// the authoritative value explicitly instead of conflating the two wires.
	req.Identity.ParentSessionID = authoritative.Identity.ParentSessionID
	// The same split: the authoritative model keys harnessVersion, the legacy
	// model keeps version.
	req.Model = schema.ModelInfo(authoritative.Model)
	return req, stored, nil
}
