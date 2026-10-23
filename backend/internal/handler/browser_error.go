package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/peasant-labs/village/backend/internal/browserpage"
)

// failBrowser answers a failed route that a browser navigates to: a sign-in or
// install callback, or the CLI's login start. The status is the same either
// way. A request that asks for HTML first (a person's tab) gets the page; any
// other client gets the JSON error it always got.
func (h *Handler) failBrowser(w http.ResponseWriter, r *http.Request, status int, apiMessage string, page browserpage.Page) {
	if !browserpage.PrefersHTML(r) {
		writeError(w, status, apiMessage)
		return
	}
	_ = browserpage.Render(w, status, page)
}

// frontendHref is an address on the hosted app.
func (h *Handler) frontendHref(path string) string {
	return strings.TrimRight(h.cfg.FrontendURL, "/") + path
}

// signInFailed is the page for a web sign-in that did not finish. It sends the
// person back to the sign-in, which starts a fresh handshake.
func (h *Handler) signInFailed(detail string) browserpage.Page {
	return browserpage.Page{
		Title:   "sign-in failed | peasant",
		Heading: "the sign-in did not finish",
		Detail:  detail,
		Status:  "not signed in",
		Action:  "sign in again",
		Href:    h.frontendHref("/login"),
		Hint:    "nothing was changed on your account.",
	}
}

// cliLoginFailed is the page for a terminal login that did not finish. The
// login can only be restarted from the terminal, so it offers no link.
func cliLoginFailed(detail string) browserpage.Page {
	return browserpage.Page{
		Title:   "login failed | peasant",
		Heading: "the login did not finish",
		Detail:  detail,
		Status:  "not logged in",
		Hint:    "close this tab and log in again from peasant.",
	}
}

// installFailed is the page for a GitHub App installation that did not link.
func (h *Handler) installFailed(detail string) browserpage.Page {
	return browserpage.Page{
		Title:   "github app not linked | peasant",
		Heading: "the github app could not be linked",
		Detail:  detail,
		Action:  "back to collectives",
		Href:    h.frontendHref("/collectives"),
		Hint:    "you can start the installation again from a collective's settings.",
	}
}

// Details shared by the sign-in callbacks.
const (
	detailSignInStateLost   = "the sign-in took too long, or it was started in another tab."
	detailSignInSessionFail = "peasant online could not start your session. try again in a moment."
)

// providerDidNotFinish names the sign-in provider that did not complete the
// handshake. A provider that reports the person cancelled gets that said
// plainly instead.
func providerDidNotFinish(r *http.Request, provider string) string {
	if r.URL.Query().Get("error") == "access_denied" {
		return fmt.Sprintf("the sign-in was cancelled on %s.", provider)
	}
	return fmt.Sprintf("%s did not complete the sign-in.", provider)
}

// providerUnavailable is the detail for a provider this server has no
// credentials for.
func providerUnavailable(provider string) string {
	return fmt.Sprintf("sign-in with %s is not available on this server.", provider)
}

// Details for the terminal login.
const (
	detailCLILinkIncomplete = "this login link is incomplete, so it cannot reach your terminal."
	detailCLIStartFail      = "peasant online could not start the login. try again in a moment."
	detailCLIHandBackFail   = "peasant online could not hand the login back to your terminal. try again in a moment."
)

// Details for the GitHub App installation.
const (
	detailInstallUnavailable  = "this server is not set up to install the github app."
	detailInstallNoCollective = "the link did not say which collective to install the github app for."
	detailInstallSignedOut    = "you were signed out before the installation came back. sign in, then start it again."
	detailInstallUnread       = "github did not send back an installation peasant online could read."
	detailInstallTryAgain     = "peasant online could not record the installation. try again in a moment."
)
