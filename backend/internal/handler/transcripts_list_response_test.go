package handler

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
)

// transcriptListResponseFields is the contract's top-level field list, in
// declaration order. It is a manifest, not a tally: a renamed or dropped field
// fails by name.
//
//go:embed testdata/transcript_list_response_fields.yaml
var transcriptListResponseFieldsYAML []byte

// jsonFieldNames returns the JSON name of every exported field of a struct
// type, in declaration order. The contract types carry an explicit json tag on
// every field, so the tag is the wire name.
func jsonFieldNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// TestTranscriptListResponseMatchesTheContract proves the handler's envelope
// carries exactly the fields the contract declares, in the same order, and that
// an empty envelope round-trips into the contract type and validates. A map
// envelope could silently rename a field; a typed one plus this assertion
// cannot.
func TestTranscriptListResponseMatchesTheContract(t *testing.T) {
	want, err := decodeFixtureRows[string](transcriptListResponseFieldsYAML)
	if err != nil {
		t.Fatalf("load the response field manifest: %v", err)
	}
	if len(want) == 0 {
		t.Fatal("the response field manifest is empty")
	}
	if got := jsonFieldNames(reflect.TypeOf(transcriptListResponse{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("handler envelope fields = %v, want the contract's %v", got, want)
	}
	if got := jsonFieldNames(reflect.TypeOf(schema.VillageTranscriptListResponse{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("schema.VillageTranscriptListResponse fields = %v, want the manifest's %v; the contract moved and the handler did not", got, want)
	}

	// The empty arrays are part of the contract: a nil slice would decode as
	// null, which the contract's Validate refuses. This also proves every field
	// name above decodes into the contract type (a renamed field would leave the
	// contract's field at its zero value and fail validation).
	body, err := json.Marshal(transcriptListResponse{
		Transcripts: []map[string]any{},
		NextItems:   []map[string]any{},
		Page:        1,
		PerPage:     25,
	})
	if err != nil {
		t.Fatalf("marshal the empty envelope: %v", err)
	}
	var decoded schema.VillageTranscriptListResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode the empty envelope into schema.VillageTranscriptListResponse: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("the emitted envelope does not satisfy schema.VillageTranscriptListResponse.Validate: %v", err)
	}
	if decoded.PerPage != 25 || decoded.Page != 1 {
		t.Fatalf("decoded page/per_page = %d/%d, want 1/25; the wire names do not map onto the contract", decoded.Page, decoded.PerPage)
	}
}
