package github

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/pull_request.yaml
var pullRequestYAML []byte

// requiredPullRequestCaseNames is the name manifest for the fixture: the fork
// case and the same-repository case, so a decode that cannot tell them apart
// fails here.
var requiredPullRequestCaseNames = []string{"fork-pull-request", "same-repository-pull-request"}

type pullRequestCase struct {
	Name     string `yaml:"name"`
	Response string `yaml:"response"`
	Expect   struct {
		Number           int    `yaml:"number"`
		RepoID           int64  `yaml:"repo_id"`
		HeadSHA          string `yaml:"head_sha"`
		HeadRef          string `yaml:"head_ref"`
		HeadRepo         string `yaml:"head_repo"`
		HeadRepoFullName string `yaml:"head_repo_full_name"`
		BaseRepo         string `yaml:"base_repo"`
		AuthorID         int64  `yaml:"author_id"`
		IsFork           bool   `yaml:"is_fork"`
		State            string `yaml:"state"`
	} `yaml:"expect"`
}

type pullRequestFile struct {
	Cases []pullRequestCase `yaml:"cases"`
}

func loadPullRequestCases(t *testing.T) []pullRequestCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(pullRequestYAML))
	decoder.KnownFields(true)
	var file pullRequestFile
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("decode the pull request fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("the pull request fixture must hold one document: %v", err)
	}
	present := map[string]bool{}
	for _, c := range file.Cases {
		if present[c.Name] {
			t.Fatalf("the pull request fixture repeats %q", c.Name)
		}
		present[c.Name] = true
	}
	var missing, undeclared []string
	declared := map[string]bool{}
	for _, name := range requiredPullRequestCaseNames {
		declared[name] = true
	}
	for name := range declared {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	for name := range present {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/pull_request.yaml omits %v, which its manifest declares", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/pull_request.yaml carries undeclared case(s) %v", undeclared)
	}
	return file.Cases
}

// TestGetPullRequestDecodesTheDocumentedShape checks the pull request read
// against a captured response, so the fields the lifecycle depends on are
// pinned by the wire shape rather than by the struct that decodes it.
func TestGetPullRequestDecodesTheDocumentedShape(t *testing.T) {
	for _, tc := range loadPullRequestCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/app/installations/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, `{"token":"ghs_test","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			})
			mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, tc.Response)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			client := newTestClient(t, srv.URL)
			pr, err := client.GetPullRequest(context.Background(), 42, "peasant-labs", "village", tc.Expect.Number)
			if err != nil {
				t.Fatalf("GetPullRequest: %v", err)
			}
			if pr.Number != tc.Expect.Number || pr.RepoID != tc.Expect.RepoID || pr.HeadSHA != tc.Expect.HeadSHA ||
				pr.HeadRef != tc.Expect.HeadRef || pr.HeadRepo != tc.Expect.HeadRepo ||
				pr.HeadRepoFullName != tc.Expect.HeadRepoFullName || pr.BaseRepo != tc.Expect.BaseRepo ||
				pr.AuthorID != tc.Expect.AuthorID || pr.IsFork != tc.Expect.IsFork || pr.State != tc.Expect.State {
				t.Fatalf("decoded pull request = %+v, want %+v", pr, tc.Expect)
			}
		})
	}
}
