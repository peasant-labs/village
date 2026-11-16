package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mountCallees are the router methods whose first argument is a route pattern.
// A group-relative pattern (starts with "/" and is outside every configured
// endpoint prefix) is visible to the mount rule only when its file is listed.
var mountCallees = map[string]struct{}{
	"Get":        {},
	"Post":       {},
	"Put":        {},
	"Patch":      {},
	"Delete":     {},
	"Head":       {},
	"Options":    {},
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

// TestUnstringMountFilesCoverRouteMounts fails when a production router file
// mounts a group-relative route and is missing from endpoints.mountFiles.
// The live tree is the positive case: router.go lists /health, which is
// outside the default /api/v1 prefix, and that file is already listed.
func TestUnstringMountFilesCoverRouteMounts(t *testing.T) {
	root := repoRoot(t)
	cfg := readUnstringConfig(t, filepath.Join(root, ".unstring.yaml"))
	prefixes := cfg.Endpoints.Prefixes
	if len(prefixes) == 0 {
		// unstring-allow: go-endpoint-literal — config default when prefixes are empty, not a route
		prefixes = []string{"/api/v1"}
	}
	listed := make(map[string]struct{}, len(cfg.Endpoints.MountFiles))
	for _, name := range cfg.Endpoints.MountFiles {
		listed[filepath.ToSlash(name)] = struct{}{}
	}

	routerDir := filepath.Join(root, "backend", "internal", "router")
	entries, err := os.ReadDir(routerDir)
	if err != nil {
		t.Fatalf("read %s: %v; the mount-file check cannot see route files", routerDir, err)
	}

	var scanned, inScope int
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		rel := filepath.ToSlash(filepath.Join("backend", "internal", "router", name))
		path := filepath.Join(routerDir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v; a router file that does not parse cannot be checked against endpoints.mountFiles", rel, err)
		}
		_, known := listed[rel]
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
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s:%d: route literal %s could not be unquoted (%v); the mount-file check only accepts a Go string literal", rel, fset.Position(lit.Pos()).Line, lit.Value, err)
				return true
			}
			if !groupRelative(value, prefixes) {
				return true
			}
			inScope++
			if known {
				return true
			}
			pos := fset.Position(lit.Pos())
			t.Errorf("%s:%d: route literal %q is group-relative, so the mount rule ignores it unless the file is listed in endpoints.mountFiles; add %q to endpoints.mountFiles", rel, pos.Line, value, rel)
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no production Go files under backend/internal/router; the mount-file check would pass without seeing a router")
	}
	if inScope == 0 {
		t.Fatal("no group-relative route literal in backend/internal/router; the live tree should mount /health outside the endpoint prefix, so this pass would be vacuous")
	}
}

func groupRelative(pattern string, prefixes []string) bool {
	if !strings.HasPrefix(pattern, "/") {
		return false
	}
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(pattern, prefix) {
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
