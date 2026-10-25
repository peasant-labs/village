package handler

import (
	"context"
	_ "embed"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/reserved-handles.yaml
var reservedHandlesYAML []byte

type reservedHandleCase struct {
	Name     string `yaml:"name"`
	Path     string `yaml:"path"`
	Input    string `yaml:"input"`
	Reserved bool   `yaml:"reserved"`
	Want     string `yaml:"want"`
}

var requiredReservedHandleCases = []string{
	"choose_reserved", "choose_reserved_upper_case", "confirm_reserved_login", "generate_from_reserved_login",
}

func TestReservedHandlesAreNeverAssigned(t *testing.T) {
	cases, err := decodeFixtureRows[reservedHandleCase](reservedHandlesYAML)
	if err != nil {
		t.Fatalf("load the reserved-handle corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		present[c.Name] = true
	}
	for _, name := range requiredReservedHandleCases {
		if !present[name] {
			t.Fatalf("the reserved-handle corpus no longer contains %q; restore it", name)
		}
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			switch c.Path {
			case "choose":
				set := false
				q := &mockQuerier{setUsername: func(_ context.Context, arg sqlc.SetUsernameParams) (sqlc.User, error) {
					set = true
					return sqlc.User{ID: arg.ID, GithubUsername: arg.GithubUsername}, nil
				}}
				h := newTestHandler(q, nil)
				r := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me/username", strings.NewReader(`{"username":"`+c.Input+`"}`))
				r = r.WithContext(withUserID(r.Context(), uuid.New()))
				w := httptest.NewRecorder()
				h.SetMyUsername(w, r)
				if c.Reserved {
					if w.Code != http.StatusBadRequest || set || !strings.Contains(w.Body.String(), "reserved") {
						t.Fatalf("choosing %q answered %d (stored=%v): %s; want 400 reserved and nothing stored", c.Input, w.Code, set, w.Body.String())
					}
					return
				}
				if w.Code != http.StatusOK || !set {
					t.Fatalf("choosing %q answered %d (stored=%v): %s", c.Input, w.Code, set, w.Body.String())
				}
			case "confirm":
				if got := handleIsLogin(strings.ToLower(c.Input), c.Input); got == c.Reserved {
					t.Fatalf("handleIsLogin(%q) = %v, want %v", c.Input, got, !c.Reserved)
				}
			case "generate":
				h := newTestHandler(&mockQuerier{getUserByUsername: func(context.Context, string) (sqlc.User, error) {
					return sqlc.User{}, errors.New("no rows")
				}}, nil)
				if got := h.generateUniqueUsername(context.Background(), c.Input); got != c.Want {
					t.Fatalf("generateUniqueUsername(%q) = %q, want %q", c.Input, got, c.Want)
				}
			default:
				t.Fatalf("case %q names path %q; the paths are choose, confirm and generate", c.Name, c.Path)
			}
		})
	}
}
