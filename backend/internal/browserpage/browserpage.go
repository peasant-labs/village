// Package browserpage renders the small HTML page a browser gets when a route
// it navigates to (a sign-in or install callback, the CLI's login start)
// fails. The API answers everything else in JSON; these routes are opened in
// a tab, so a person would otherwise read a raw JSON error.
//
// The page cannot load the frontend's stylesheet, so it carries a small inline
// one in the fairtrade look. Its colors are read at startup from
// fairtrade-tokens.json, a byte copy of the tokens.json the frontend's pinned
// @peasant-labs/fairtrade publishes; a test keeps the copy equal to the
// installed package. Dark is the default and light follows the system's
// prefers-color-scheme.
package browserpage

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
)

// Page is what a failed browser route shows: the peasant wordmark, a heading,
// one line of detail, an optional state line, and an optional way on.
type Page struct {
	Title   string
	Heading string
	Detail  string
	// Status is an optional short state line, such as "not signed in".
	Status string
	// Action and Href are an optional link, such as back to sign-in.
	Action string
	Href   string
	// Hint is the closing line in the page's footer.
	Hint string
}

//go:embed fairtrade-tokens.json
var tokensJSON []byte

//go:embed page.html.tmpl
var pageTemplateSource string

var pageTemplate = template.Must(template.New("page").Parse(pageTemplateSource))

// paletteTokens are the fairtrade color tokens the page paints with, each
// emitted as a CSS custom property of the same name.
var paletteTokens = []string{
	"canvas", "surface", "surface-2", "ink-strong", "ink", "ink-2", "ink-3",
	"rule", "rule-strong", "amber", "danger", "focus-ring",
}

// contentSecurityPolicy lets the page load its inline style and the Atkinson
// fonts and nothing else: no script, no frame, no form.
const contentSecurityPolicy = "default-src 'none'; style-src 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// pageRules is the stylesheet after the color custom properties. Every color
// is a custom property, radius is 0 everywhere, and the sizes are fairtrade's
// spacing and type scale.
const pageRules = `
*,*::before,*::after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%;text-size-adjust:100%}
body{margin:0;min-height:100vh;min-height:100dvh;display:grid;place-items:center;padding:32px 16px;background:var(--canvas);color:var(--ink);font-family:"Atkinson Hyperlegible",ui-sans-serif,system-ui,-apple-system,sans-serif;font-size:16px;line-height:1.5;letter-spacing:.03em;word-spacing:.16em;-webkit-font-smoothing:antialiased}
.pg-card{width:min(27.5rem,100%);border:1px solid var(--rule-strong);background:var(--surface)}
.pg-card>header,.pg-card>footer{padding:20px 24px}
.pg-card>header{display:grid;gap:16px;border-bottom:1px solid var(--rule)}
.pg-card>footer{border-top:1px solid var(--rule)}
.pg-brand,.pg-card h1,.pg-status,.pg-hint,.pg-action{font-family:"Atkinson Hyperlegible Mono",ui-monospace,Menlo,Consolas,monospace;letter-spacing:0;word-spacing:0}
.pg-brand{display:inline-flex;align-items:center;gap:10px;font-weight:700;font-size:16px;color:var(--ink-strong)}
.pg-brand svg{flex:none;width:18px;height:22px;color:var(--amber)}
.pg-card h1{margin:0;font-weight:700;font-size:22px;line-height:1.2;color:var(--ink-strong)}
.pg-body{display:grid;gap:20px;padding:24px}
.pg-body p{margin:0}
.pg-detail{color:var(--ink-2)}
.pg-status{display:flex;align-items:center;gap:8px;font-size:14px;color:var(--ink)}
.pg-dot{flex:none;width:8px;height:8px;background:var(--danger)}
.pg-action{justify-self:start;display:inline-flex;align-items:center;min-height:44px;padding:0 16px;border:1px solid var(--rule-strong);color:var(--ink-strong);font-size:14px;text-decoration:none}
.pg-action:hover{background:var(--surface-2)}
.pg-action:focus-visible{outline:3px solid var(--focus-ring);outline-offset:2px}
.pg-hint{margin:0;font-size:14px;color:var(--ink-3)}
`

// stylesheet is built once from the embedded tokens.
var stylesheet = mustBuildStylesheet(tokensJSON)

type colorToken struct {
	Value      string `json:"$value"`
	Extensions struct {
		Theme struct {
			Light string `json:"light"`
		} `json:"fairtrade.theme"`
	} `json:"$extensions"`
}

// mustBuildStylesheet writes the dark palette as :root custom properties, the
// light palette under prefers-color-scheme: light, then the page rules. A token
// missing from the snapshot, or missing a side, is a broken snapshot and stops
// the server at startup rather than painting a page without it.
func mustBuildStylesheet(tokens []byte) string {
	var doc struct {
		Color map[string]colorToken `json:"color"`
	}
	if err := json.Unmarshal(tokens, &doc); err != nil {
		panic(fmt.Sprintf("browserpage: parse fairtrade-tokens.json: %v", err))
	}
	var dark, light strings.Builder
	for _, name := range paletteTokens {
		token, ok := doc.Color[name]
		if !ok || token.Value == "" || token.Extensions.Theme.Light == "" {
			panic(fmt.Sprintf("browserpage: fairtrade-tokens.json has no dark and light value for color %q; re-copy it from the frontend's installed @peasant-labs/fairtrade", name))
		}
		fmt.Fprintf(&dark, ";--%s:%s", name, strings.ToLower(token.Value))
		fmt.Fprintf(&light, ";--%s:%s", name, strings.ToLower(token.Extensions.Theme.Light))
	}
	return ":root{color-scheme:dark" + dark.String() + "}\n" +
		"@media (prefers-color-scheme:light){:root{color-scheme:light" + light.String() + "}}" +
		pageRules
}

// Render writes p as a complete HTML document with the given status. Every
// field is escaped by html/template.
func Render(w http.ResponseWriter, status int, p Page) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	return write(w, p)
}

func write(w io.Writer, p Page) error {
	return pageTemplate.Execute(w, struct {
		Page
		Stylesheet template.CSS
	}{
		Page: p,
		// Built only from the embedded tokens and the constant rules above,
		// never from request data.
		Stylesheet: template.CSS(stylesheet),
	})
}

// PrefersHTML reports whether a request asks for HTML first, as a browser
// navigating to a URL does. A fetch or an API client does not, and keeps the
// JSON error.
func PrefersHTML(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		mediaType := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		switch mediaType {
		case "text/html", "application/xhtml+xml":
			return true
		case "application/json":
			return false
		}
	}
	return false
}
