package handler

import (
	_ "embed"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/helper_scope_cache.yaml
var helperScopeCacheYAML []byte

type scopeCacheFixtures struct {
	Capacity int `yaml:"capacity"`
	Lifetime int `yaml:"lifetime_minutes"`
	Cases    []struct {
		Name    string `yaml:"name"`
		Viewer  string `yaml:"viewer"`
		Group   string `yaml:"group"`
		Elapsed int    `yaml:"elapsed_minutes"`
		Expired bool   `yaml:"expired"`
	} `yaml:"cases"`
}

func loadScopeCacheFixtures(t *testing.T) scopeCacheFixtures {
	t.Helper()
	var fixture scopeCacheFixtures
	if err := yaml.Unmarshal(helperScopeCacheYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range fixture.Cases {
		if names[c.Name] || c.Name == "" {
			t.Fatalf("invalid cache fixture name %q", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range []string{"exact-binding", "expired-at-boundary", "foreign-viewer-binding", "foreign-group-binding", "filter-copy-no-alias"} {
		if !names[name] {
			t.Fatalf("missing cache case %s", name)
		}
	}
	if fixture.Capacity != groupedScopeCapacity || time.Duration(fixture.Lifetime)*time.Minute != groupedScopeLifetime {
		t.Fatal("cache resource contract changed")
	}
	return fixture
}

func TestGroupedScopeBindingAndOwnership(t *testing.T) {
	fixture := loadScopeCacheFixtures(t)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var cache groupedScopeService
			now := time.Unix(100, 0)
			request := GroupedScopeRequest{Variant: GroupedRouteTranscripts, ViewerID: "viewer", Query: "exact query", Tags: []string{"original"}}
			token, err := cache.mint(request, "hg_group", now)
			if err != nil {
				t.Fatal(err)
			}
			request.Tags[0] = "mutated caller"
			got, err := cache.lookup(token, c.Group, c.Viewer, now.Add(time.Duration(c.Elapsed)*time.Minute))
			if errors.Is(err, ErrGroupScopeExpired) != c.Expired {
				t.Fatalf("scope error %v, expected expired=%t", err, c.Expired)
			}
			if err != nil {
				return
			}
			if got.Query != "exact query" || got.Tags[0] != "original" {
				t.Fatal("original parsed filters were aliased or lost")
			}
			got.Tags[0] = "mutated reader"
			again, err := cache.lookup(token, c.Group, c.Viewer, now)
			if err != nil || again.Tags[0] != "original" {
				t.Fatal("lookup exposed mutable cached filters")
			}
		})
	}
}

func TestGroupedScopeBoundedConcurrentAccess(t *testing.T) {
	fixture := loadScopeCacheFixtures(t)
	var cache groupedScopeService
	now := time.Unix(100, 0)
	var wg sync.WaitGroup
	errorsFound := make(chan error, fixture.Capacity+1)
	for i := 0; i < fixture.Capacity+1; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := cache.mint(GroupedScopeRequest{Variant: GroupedRouteTranscripts}, "hg_group", now)
			if err != nil {
				errorsFound <- err
				return
			}
			_, err = cache.lookup(token, "hg_group", "", now)
			// Eviction is valid while another concurrent list claims capacity.
			if err != nil && !errors.Is(err, ErrGroupScopeExpired) {
				errorsFound <- err
			}
		}()
	}
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != fixture.Capacity {
		t.Fatalf("cache entry bound=%d want=%d", len(cache.entries), fixture.Capacity)
	}
}
