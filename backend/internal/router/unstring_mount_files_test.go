package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mountCallees are the chi router methods that take a route pattern. Every
// callee but Method puts that pattern in the first argument. Method takes the
// HTTP method first and the pattern second: r.Method("GET", "/probe", h).
// Trace and Connect take the pattern first, the same as Get.
// A group-relative pattern (starts with "/" and is outside every configured
// endpoint prefix) is visible to the mount rule only when its file is listed.
var mountCallees = map[string]struct{}{
	"Connect":    {},
	"Get":        {},
	"Post":       {},
	"Put":        {},
	"Patch":      {},
	"Delete":     {},
	"Head":       {},
	"Options":    {},
	"Trace":      {},
	"Route":      {},
	"Mount":      {},
	"Handle":     {},
	"HandleFunc": {},
	"Method":     {},
}

type unstringConfig struct {
	Endpoints struct {
		MountFiles []string `yaml:"mountFiles"`
		Prefixes   []string `yaml:"prefixes"`
	} `yaml:"endpoints"`
}

// TestUnstringMountFilesCoverRouteMounts requires coverage for every router
// file with a mount call, even when all current patterns use contract members.
func TestUnstringMountFilesCoverRouteMounts(t *testing.T) {
	root := repoRoot(t)
	cfg := readUnstringConfig(t, filepath.Join(root, ".unstring.yaml"))
	mountEntries := make([]string, len(cfg.Endpoints.MountFiles))
	for i, name := range cfg.Endpoints.MountFiles {
		mountEntries[i] = filepath.ToSlash(name)
	}

	routerDir := filepath.Join(root, "backend", "internal", "router")
	entries, err := os.ReadDir(routerDir)
	if err != nil {
		t.Fatalf("read %s: %v; the mount-file check cannot see route files", routerDir, err)
	}

	var scanned, mounts int
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		rel := filepath.ToSlash(filepath.Join("backend", "internal", "router", name))
		src := filepath.Join(routerDir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, src, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v; a router file that does not parse cannot be checked against endpoints.mountFiles", rel, err)
		}
		known, err := mountEntryCovers(mountEntries, rel)
		if err != nil {
			t.Fatalf("match %s against endpoints.mountFiles: %v; an entry must be a path or a glob path.Match accepts", rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, ok := mountCallees[sel.Sel.Name]; !ok {
				return true
			}
			mounts++
			if !known {
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d: route mount is outside endpoints.mountFiles; add %q or a matching glob", rel, pos.Line, rel)
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no production Go files under backend/internal/router; the mount-file check would pass without seeing a router")
	}
	if mounts == 0 {
		t.Fatal("no route mount calls found; the mount-file check would be vacuous")
	}
}

// mountEntryCovers reports whether rel matches any endpoints.mountFiles entry.
// An entry is an exact repo-relative path or a glob. path.Match treats * as
// any run of non-slash characters, so backend/internal/router/*.go covers
// router.go and not a file in a subdirectory. The live config does not use **.
func mountEntryCovers(entries []string, rel string) (bool, error) {
	for _, entry := range entries {
		ok, err := path.Match(entry, rel)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// groupRelative reports whether pattern is a route the mount rule sees only in
// a listed file: it starts with "/" and sits outside every endpoint prefix.
// A pattern is under a prefix only when it equals that prefix or starts with
// prefix+"/", so /api/v10/x is not under /api/v1. A bare HasPrefix would hide
// that route and let an unlisted file escape.
func groupRelative(pattern string, prefixes []string) bool {
	if !strings.HasPrefix(pattern, "/") {
		return false
	}
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		if pattern == prefix || strings.HasPrefix(pattern, prefix+"/") {
			return false
		}
	}
	return true
}

func readUnstringConfig(t *testing.T, path string) unstringConfig {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v; the mount-file check needs the repository config", path, err)
	}
	var cfg unstringConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse %s: %v; endpoints.mountFiles must be a list of repo-relative paths", path, err)
	}
	return cfg
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the router package to walk up to .unstring.yaml")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".unstring.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to %s without finding .unstring.yaml; run the test from the village module tree", file)
		}
		dir = parent
	}
}
