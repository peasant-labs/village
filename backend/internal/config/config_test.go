package config

import (
	"strings"
	"testing"
)

func TestValidateJWTSecret_Empty(t *testing.T) {
	err := validateJWTSecret("")
	if err == nil {
		t.Fatal("expected error for empty secret, got nil")
	}
}

func TestValidateJWTSecret_Blocklisted(t *testing.T) {
	blocklisted := []string{
		"change-me-in-production",
		"dev-jwt-secret-change-in-prod",
	}
	for _, secret := range blocklisted {
		t.Run(secret, func(t *testing.T) {
			err := validateJWTSecret(secret)
			if err == nil {
				t.Fatalf("expected error for blocklisted secret %q, got nil", secret)
			}
			if !strings.Contains(err.Error(), "known-weak") {
				t.Errorf("expected error message to mention 'known-weak', got: %s", err.Error())
			}
		})
	}
}

func TestValidateJWTSecret_TooShort(t *testing.T) {
	// 31 characters — one below the minimum of 32
	short := strings.Repeat("x", jwtSecretMinLength-1)
	err := validateJWTSecret(short)
	if err == nil {
		t.Fatalf("expected error for %d-char secret, got nil", len(short))
	}
	if !strings.Contains(err.Error(), "at least") {
		t.Errorf("expected error message to mention 'at least', got: %s", err.Error())
	}
}

func TestValidateJWTSecret_ExactMinLength(t *testing.T) {
	// Exactly 32 characters — should pass
	exact := strings.Repeat("x", jwtSecretMinLength)
	err := validateJWTSecret(exact)
	if err != nil {
		t.Fatalf("expected no error for %d-char secret, got: %v", len(exact), err)
	}
}

func TestValidateJWTSecret_Valid(t *testing.T) {
	// Well above minimum — should pass
	strong := strings.Repeat("a", 64)
	err := validateJWTSecret(strong)
	if err != nil {
		t.Fatalf("expected no error for 64-char secret, got: %v", err)
	}
}

func TestNormalizePEM(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"already multiline unchanged", "-----BEGIN-----\nbody\n-----END-----", "-----BEGIN-----\nbody\n-----END-----"},
		{"escaped newlines expanded", `-----BEGIN-----\nbody\n-----END-----`, "-----BEGIN-----\nbody\n-----END-----"},
		{"no newlines at all unchanged", "plain", "plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePEM(tc.in); got != tc.want {
				t.Errorf("normalizePEM(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
