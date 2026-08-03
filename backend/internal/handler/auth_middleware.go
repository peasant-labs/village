package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
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

func (h *Handler) AuthRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := h.authenticate(r)
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

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
	// 1. Cookie
	if cookie, err := r.Cookie(auth.CookieName); err == nil {
		claims, err := auth.ValidateToken(h.cfg.JWTSecret, cookie.Value)
		if err == nil {
			return &AuthUser{ID: claims.UserID, Username: claims.Username}, nil
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
	return &AuthUser{ID: claims.UserID, Username: claims.Username}, nil
}
