package auth

import (
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

func GitHubOAuthConfig(clientID, clientSecret, callbackURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     github.Endpoint,
		RedirectURL:  callbackURL,
		Scopes:       []string{"read:user", "read:org"},
	}
}

func GitLabOAuthConfig(clientID, clientSecret, callbackURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://gitlab.com/oauth/authorize",
			TokenURL: "https://gitlab.com/oauth/token",
		},
		RedirectURL: callbackURL,
		Scopes:      []string{"read_user"},
	}
}

func HuggingFaceOAuthConfig(clientID, clientSecret, callbackURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://huggingface.co/oauth/authorize",
			TokenURL: "https://huggingface.co/oauth/token",
		},
		RedirectURL: callbackURL,
		Scopes:      []string{"openid", "profile"},
	}
}

// CodebergOAuthConfig — Codeberg runs Forgejo (Gitea fork) which exposes
// standard /login/oauth/{authorize,access_token} endpoints. The "openid"
// scope plus "profile" gives us the /login/oauth/userinfo response.
func CodebergOAuthConfig(clientID, clientSecret, callbackURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://codeberg.org/login/oauth/authorize",
			TokenURL: "https://codeberg.org/login/oauth/access_token",
		},
		RedirectURL: callbackURL,
		Scopes:      []string{"openid", "profile"},
	}
}

// SourceHutOAuthConfig — meta.sr.ht prohibits passing redirect_uri on the
// authorize URL (it uses the URI registered with the client), and requires
// at least one scope. We request "meta.sr.ht/PROFILE:RO" which gives access
// to read the user's profile via the GraphQL `me` query.
func SourceHutOAuthConfig(clientID, clientSecret, callbackURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://meta.sr.ht/oauth2/authorize",
			TokenURL: "https://meta.sr.ht/oauth2/access-token",
		},
		RedirectURL: callbackURL,
		Scopes:      []string{"meta.sr.ht/PROFILE:RO"},
	}
}
