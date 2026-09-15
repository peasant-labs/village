package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// InstallStateTTL is how long a GitHub App install-handshake state token stays
// valid. It only needs to survive one trip to GitHub and back.
const InstallStateTTL = 15 * time.Minute

// InstallStateClaims binds an install handshake to the signed-in caller and the
// collective they are installing for, so the callback cannot be driven by anyone
// else or pointed at another collective.
type InstallStateClaims struct {
	UserID  string `json:"user_id"`
	GroupID string `json:"group_id"`
	jwt.RegisteredClaims
}

// CreateInstallState signs a short-lived state token for the install handshake.
func CreateInstallState(secret, userID, groupID string) (string, error) {
	claims := InstallStateClaims{
		UserID:  userID,
		GroupID: groupID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(InstallStateTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ValidateInstallState verifies an install-handshake state token and returns its
// claims. A wrong, forged, or expired token is refused.
func ValidateInstallState(secret, tokenStr string) (*InstallStateClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &InstallStateClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*InstallStateClaims)
	if !ok || !token.Valid || claims.UserID == "" || claims.GroupID == "" {
		return nil, fmt.Errorf("invalid install state")
	}
	return claims, nil
}
