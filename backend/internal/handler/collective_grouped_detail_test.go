package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// The stored pull-request check mode is read from the collective row and served
// on the grouped detail body, so it is a trust boundary: a value outside the
// canonical menu must be refused with an actionable error instead of being
// served to a client that cannot interpret it, or replaced by a default that
// would silently change the collective's pull-request behaviour.
func TestCollectiveGroupedDetailRefusesUnsupportedStoredCheckMode(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/groups/52000000-0000-4000-8000-000000000001?view=grouped", nil)
	h := &Handler{}
	group := sqlc.Group{PromptsCheckMode: "invented"}

	response, err := h.collectiveGroupedDetail(r, group, "owner", true, schema.VillageSessionListPayload{})

	var refusal *GroupedScopeError
	if !errors.As(err, &refusal) {
		t.Fatalf("an unsupported stored check mode must be refused, got response=%+v err=%v", response.Group, err)
	}
	if refusal.Status != http.StatusInternalServerError {
		t.Fatalf("refusal status = %d, want %d", refusal.Status, http.StatusInternalServerError)
	}
	for _, want := range []string{"collectiveGroupedDetail", "outside the supported menu", "no transcript list or members were returned"} {
		if !strings.Contains(refusal.Message, want) {
			t.Fatalf("refusal must name the failing step, the reason and the caller effect; %q is missing from %q", want, refusal.Message)
		}
	}
}
