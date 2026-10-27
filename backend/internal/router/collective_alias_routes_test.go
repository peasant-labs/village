package router

import (
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

//go:embed testdata/collective_alias_routes.yaml
var collectiveAliasRoutesYAML []byte

type collectiveAliasRoute struct {
	Name   string `yaml:"name"`
	Method string `yaml:"method"`
	Suffix string `yaml:"suffix"`
	Auth   string `yaml:"auth"`
	Actor  string `yaml:"actor"`
	Body   string `yaml:"body"`
	Status int    `yaml:"status"`
}

func collectiveAliasRoutes(t *testing.T) []collectiveAliasRoute {
	t.Helper()
	rows, err := decodeSingleYAMLDocument[[]collectiveAliasRoute](collectiveAliasRoutesYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"public-collectives": false, "search-collectives-query": false, "create-collective-body": false, "membership-list": false, "visible-list": false,
		"collective-detail-id": false, "update-collective-body": false, "delete-collective-owner-refusal": false, "join-existing-membership": false,
		"invite-body": false, "invitations-list": false, "member-role-user-id-body": false, "remove-member-owner-refusal": false, "contributable-flat": false,
		"batch-share-body": false, "pending-query": false, "my-shares-query": false, "batch-review-body": false, "single-review-transcript-id-body": false,
		"remove-transcript-owner-refusal": false, "repository-list": false, "link-repository-config-refusal": false,
		"available-repositories-query": false, "unlink-repository-parameters": false, "repository-commits-query": false,
	}
	for _, row := range rows {
		if _, ok := required[row.Name]; !ok || required[row.Name] {
			t.Fatalf("unknown/repeated alias route case %q", row.Name)
		}
		required[row.Name] = true
		if row.Auth != "optional" && row.Auth != "required" {
			t.Fatalf("unknown auth expectation %q", row.Auth)
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing required alias route %q", name)
		}
	}
	return rows
}

// Full production router, not an independently maintained mock alias router.
// Anonymous refusal must happen before any database dependency is reached.
func TestCollectiveAliasMountsAndAuth(t *testing.T) {
	router := productionRouter(t)
	mounted := map[string]bool{}
	for _, route := range enumerateAPIRoutes(t, router) {
		mounted[routeKey(route.Method, route.Path)] = true
	}
	for _, c := range collectiveAliasRoutes(t) {
		t.Run(c.Name, func(t *testing.T) {
			for _, prefix := range []string{"/api/v1/collectives", "/api/v1/groups"} {
				path := prefix + strings.SplitN(c.Suffix, "?", 2)[0]
				if !mounted[routeKey(c.Method, path)] {
					t.Fatalf("alias is not mounted: %s %s", c.Method, path)
				}
				if c.Auth != "required" {
					continue
				}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(c.Method, prefix+c.Suffix, strings.NewReader(c.Body)))
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous %s %s returned %d: %s", c.Method, path, rec.Code, rec.Body.String())
				}
			}
		})
	}
}
