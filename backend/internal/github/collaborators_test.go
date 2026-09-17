package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// readerServer answers the two calls the repository-reader fallback makes, in
// the shape GitHub answers them.
func readerServer(t *testing.T, login string, permission string, askedPath *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_installtoken","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/user/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"login":%q}`, login)
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		if askedPath != nil {
			*askedPath = r.URL.Path
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"permission":%q,"role_name":%q,"user":{"login":%q}}`, permission, permission, login)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The account id is what is asked about, and the login it currently has is what
// comes back: a login can be renamed and a freed one taken by another account, so
// a stored login is never the thing a reader's access is asked about.
func TestGetUserLogin_ResolvesTheIdToItsCurrentLogin(t *testing.T) {
	srv := readerServer(t, "octocat", "read", nil)
	c := newTestClient(t, srv.URL)

	login, err := c.GetUserLogin(context.Background(), 42, "4242")
	if err != nil {
		t.Fatalf("GetUserLogin: %v", err)
	}
	if login != "octocat" {
		t.Fatalf("login = %q, want octocat", login)
	}
}

// An account id that resolved to no login is an error rather than a name to ask
// about: asking GitHub about the empty login would answer for whatever that
// names, which is nobody this reader is.
func TestGetUserLogin_EmptyLoginIsAnError(t *testing.T) {
	srv := readerServer(t, "", "read", nil)
	c := newTestClient(t, srv.URL)

	if _, err := c.GetUserLogin(context.Background(), 42, "4242"); err == nil {
		t.Fatal("an empty login must be an error, not a name to ask repository access about")
	}
}

// The permission is passed through as GitHub states it, and the login is asked
// about at the endpoint's own path. The values are not interpreted here: what
// admits a reader is a decision the handler makes, so the client must not
// silently narrow or widen the answer.
func TestGetCollaboratorPermission_AsksForThatLoginAndPassesTheAnswerOn(t *testing.T) {
	var asked string
	srv := readerServer(t, "octocat", "write", &asked)
	c := newTestClient(t, srv.URL)

	permission, err := c.GetCollaboratorPermission(context.Background(), 42, "acme", "widgets", "octocat")
	if err != nil {
		t.Fatalf("GetCollaboratorPermission: %v", err)
	}
	if permission != "write" {
		t.Fatalf("permission = %q, want write", permission)
	}
	if want := "/repos/acme/widgets/collaborators/octocat/permission"; asked != want {
		t.Fatalf("asked %q, want %q", asked, want)
	}
}

// A refusal is an answer, not a failure: "none" comes back as it is, so the
// caller can tell a reader GitHub refused from a lookup that did not happen.
func TestGetCollaboratorPermission_NoneIsAnAnswer(t *testing.T) {
	srv := readerServer(t, "octocat", "none", nil)
	c := newTestClient(t, srv.URL)

	permission, err := c.GetCollaboratorPermission(context.Background(), 42, "acme", "widgets", "octocat")
	if err != nil {
		t.Fatalf("GetCollaboratorPermission: %v", err)
	}
	if permission != "none" {
		t.Fatalf("permission = %q, want none", permission)
	}
}
