package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/auth"
)

type contextKey string

const UserContextKey contextKey = "user"

type AuthUser struct {
	ID       uuid.UUID
	Username string
}

func (u *AuthUser) PgID() pgtype.UUID {
	return pgtype.UUID{Bytes: u.ID, Valid: true}
}

func GetUser(ctx context.Context) *AuthUser {
	u, _ := ctx.Value(UserContextKey).(*AuthUser)
	return u
}

// errSessionAccountUnconfirmed reports that a session cookie or bearer token
// carried a valid signature, but Village could not read the account it names,
// so it cannot tell whether that account still exists. The request is never
// treated as signed in: auth-required routes answer 503 and optional-auth
// routes serve it as anonymous.
var errSessionAccountUnconfirmed = errors.New("the session's account could not be confirmed")

// sessionAccountUnconfirmedMessage is the 503 body for errSessionAccountUnconfirmed.
const sessionAccountUnconfirmedMessage = "Could not confirm your signed-in account: the account lookup failed before the request ran, so nothing was read or changed. Retry in a few seconds; if it keeps failing, Village's database is unavailable."

// sessionAccountRetryAfterSeconds is the Retry-After value sent with the 503.
const sessionAccountRetryAfterSeconds = "5"

func (h *Handler) AuthRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := h.authenticate(r)
		if errors.Is(err, errSessionAccountUnconfirmed) {
			w.Header().Set("Retry-After", sessionAccountRetryAfterSeconds)
			writeError(w, http.StatusServiceUnavailable, sessionAccountUnconfirmedMessage)
			return
		}
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AuthOptional attaches the caller when authenticate confirms one. Any error,
// including an account lookup that failed, serves the request as anonymous:
// that is the most restrictive answer and never grants access.
func (h *Handler) AuthOptional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := h.authenticate(r)
		if user != nil {
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) authenticate(r *http.Request) (*AuthUser, error) {
	// 1. Cookie. A cookie whose account no longer exists is treated like an
	// invalid cookie: the request falls through to the Authorization header.
	if cookie, err := r.Cookie(auth.CookieName); err == nil {
		claims, err := auth.ValidateToken(h.cfg.JWTSecret, cookie.Value)
		if err == nil {
			user, err := h.sessionUser(r.Context(), claims)
			if err != nil || user != nil {
				return user, err
			}
		}
	}

	// 2. Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, nil
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		return nil, nil
	}

	// 3. API key
	if auth.IsAPIKey(token) {
		hash := auth.HashAPIKey(token)
		row, err := h.queries.GetAPIKeyByHash(r.Context(), hash)
		if err != nil {
			return nil, err
		}
		// Touch last_used asynchronously
		go func() {
			_ = h.queries.TouchAPIKeyLastUsed(context.Background(), row.ID)
		}()
		userID, _ := uuid.FromBytes(row.UserID.Bytes[:])
		return &AuthUser{ID: userID, Username: row.GithubUsername}, nil
	}

	// 4. JWT bearer token
	claims, err := auth.ValidateToken(h.cfg.JWTSecret, token)
	if err != nil {
		return nil, err
	}
	return h.sessionUser(r.Context(), claims)
}

// sessionUser confirms that the account a validly signed session token names
// still exists. A token's signature and expiry outlive the account: deleting
// the user row is what ends every session for it, so each authenticated
// request reads the row by primary key.
//
// It returns (nil, nil) when the account is gone, and an error wrapping
// errSessionAccountUnconfirmed when the lookup itself fails or the handler has
// no database to ask. It never returns a user it could not confirm.
func (h *Handler) sessionUser(ctx context.Context, claims *auth.Claims) (*AuthUser, error) {
	if h.queries == nil {
		return nil, fmt.Errorf("%w: the handler has no database to read accounts from", errSessionAccountUnconfirmed)
	}
	_, err := h.queries.GetUserByID(ctx, toPgUUID(claims.UserID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		slog.Error("session account lookup failed",
			"operation", "authenticate",
			"consequence", "the request is not treated as signed in: auth-required routes answer 503 and optional-auth routes serve it anonymously",
			"remediation", "check database connectivity; the caller can retry",
			"cause_type", fmt.Sprintf("%T", err))
		return nil, fmt.Errorf("%w: %w", errSessionAccountUnconfirmed, err)
	}
	return &AuthUser{ID: claims.UserID, Username: claims.Username}, nil
}
