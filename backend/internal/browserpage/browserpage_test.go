package browserpage

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// installedTokensPath is where the frontend's pinned fairtrade package puts
// its tokens once installed, relative to this package.
const installedTokensPath = "../../../frontend/node_modules/@peasant-labs/fairtrade/dist/lib/tokens.json"

var (
	customProp = regexp.MustCompile(`--([a-z0-9-]+):(#[0-9a-f]{6})`)
	anyColor   = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)
)

// TestStylesheetColorsComeFromTheTokens proves every color the page paints
// with is a fairtrade token: each custom property carries exactly the token's
// value in its theme, and no color appears outside them.
func TestStylesheetColorsComeFromTheTokens(t *testing.T) {
	var doc struct {
		Color map[string]colorToken `json:"color"`
	}
	if err := json.Unmarshal(tokensJSON, &doc); err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	lightMedia := "@media (prefers-color-scheme:light){"
	lightAt := strings.Index(stylesheet, lightMedia)
	if lightAt < 0 {
		t.Fatalf("stylesheet has no light block:\n%s", stylesheet)
	}
	dark := stylesheet[:strings.Index(stylesheet, "}")]
	light := stylesheet[lightAt+len(lightMedia):]
	light = light[:strings.Index(light, "}")]
	check := func(scheme, block string, want func(colorToken) string) {
		t.Helper()
		props := customProp.FindAllStringSubmatch(block, -1)
		if len(props) != len(paletteTokens) {
			t.Fatalf("%s block declares %d colors, want one per palette token (%d)", scheme, len(props), len(paletteTokens))
		}
		for _, p := range props {
			if got, want := p[2], strings.ToLower(want(doc.Color[p[1]])); got != want {
				t.Errorf("%s --%s = %s, want the token's %s", scheme, p[1], got, want)
			}
		}
	}
	check("dark", dark, func(c colorToken) string { return c.Value })
	check("light", light, func(c colorToken) string { return c.Extensions.Theme.Light })
	if colors, props := len(anyColor.FindAllString(stylesheet, -1)), len(customProp.FindAllString(stylesheet, -1)); colors != props {
		t.Errorf("stylesheet carries %d colors but %d token properties; every color must come from a token", colors, props)
	}
	if strings.Contains(stylesheet, "@import") {
		t.Errorf("stylesheet uses @import; fonts load through a <link>")
	}
}

// TestTokensSnapshotMatchesInstalledFairtrade keeps the embedded copy equal
// to the tokens.json of the fairtrade version the frontend pins. It skips,
// saying so, when the frontend's dependencies are not installed.
func TestTokensSnapshotMatchesInstalledFairtrade(t *testing.T) {
	installed, err := os.ReadFile(filepath.FromSlash(installedTokensPath))
	if os.IsNotExist(err) {
		t.Skipf("frontend dependencies are not installed (%s); run pnpm install in frontend/ to check the tokens snapshot", installedTokensPath)
	}
	if err != nil {
		t.Fatalf("read installed tokens: %v", err)
	}
	if !bytes.Equal(installed, tokensJSON) {
		t.Fatalf("internal/browserpage/fairtrade-tokens.json differs from the installed @peasant-labs/fairtrade tokens.json; copy %s over it", installedTokensPath)
	}
}

// TestRenderEscapesAndLocksDown proves the page text is escaped and the page
// is served with a policy that allows no script.
func TestRenderEscapesAndLocksDown(t *testing.T) {
	const hostile = `<script>alert("x")</script>`
	rec := httptest.NewRecorder()
	if err := Render(rec, http.StatusBadRequest, Page{
		Title: hostile, Heading: hostile, Detail: hostile, Status: hostile,
		Action: hostile, Href: "javascript:alert(1)", Hint: hostile,
	}); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if strings.Contains(body, "<script") || strings.Contains(body, "javascript:") {
		t.Fatalf("page carries unescaped markup or a script URL:\n%s", body)
	}
	if got := strings.Count(body, "&lt;script&gt;"); got != 6 {
		t.Errorf("escaped markup appears %d times, want once per text field (6)", got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") {
		t.Errorf("Content-Security-Policy = %q, want default-src 'none' and no script source", csp)
	}
}

type prefersHTMLCase struct {
	Name   string `yaml:"name"`
	Accept string `yaml:"accept"`
	HTML   bool   `yaml:"html"`
}

//go:embed testdata/prefers-html.yaml
var prefersHTMLFixture []byte

func TestPrefersHTML(t *testing.T) {
	var cases []prefersHTMLCase
	decoder := yaml.NewDecoder(bytes.NewReader(prefersHTMLFixture))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("load prefers-html fixture: %v", err)
	}
	required := map[string]bool{"browser-navigation": false, "json-first": false, "fetch-default": false, "no-accept": false}
	for _, c := range cases {
		if _, ok := required[c.Name]; ok {
			required[c.Name] = true
		}
		t.Run(c.Name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.Accept != "" {
				req.Header.Set("Accept", c.Accept)
			}
			if got := PrefersHTML(req); got != c.HTML {
				t.Fatalf("PrefersHTML(%q) = %v, want %v", c.Accept, got, c.HTML)
			}
		})
	}
	for name, present := range required {
		if !present {
			t.Errorf("prefers-html fixture lost required case %q", name)
		}
	}
}
