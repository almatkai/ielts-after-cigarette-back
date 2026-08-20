package auth

import (
	"errors"
	"testing"
	"time"
)

func testTokenManager() *TokenManager {
	return NewTokenManager(
		"0123456789abcdef0123456789abcdef",
		"issuer",
		"audience",
		time.Minute,
		time.Hour,
	)
}

func TestOAuthStateTokenRoundTrip(t *testing.T) {
	manager := testTokenManager()
	raw, err := manager.NewOAuthStateToken("github", "/app/settings")
	if err != nil {
		t.Fatalf("issue state token: %v", err)
	}
	claims, err := manager.ParseOAuthStateToken(raw)
	if err != nil {
		t.Fatalf("parse state token: %v", err)
	}
	if claims.Provider != "github" || claims.Subject != "github" {
		t.Fatalf("unexpected provider claims: %+v", claims)
	}
	if claims.Next != "/app/settings" {
		t.Fatalf("unexpected next: %q", claims.Next)
	}
	if lifetime := claims.ExpiresAt.Sub(claims.IssuedAt.Time); lifetime != 10*time.Minute {
		t.Fatalf("unexpected state token lifetime: %v", lifetime)
	}
}

func TestOAuthStateTokenRejectsTampering(t *testing.T) {
	manager := testTokenManager()
	raw, err := manager.NewOAuthStateToken("github", "/app/settings")
	if err != nil {
		t.Fatalf("issue state token: %v", err)
	}
	tampered := raw[:len(raw)-2] + "xx"
	if tampered == raw {
		tampered = raw[:len(raw)-2] + "yy"
	}
	if _, err := manager.ParseOAuthStateToken(tampered); !errors.Is(err, ErrInvalidOAuthState) {
		t.Fatalf("expected ErrInvalidOAuthState, got %v", err)
	}
	if _, err := manager.ParseOAuthStateToken(""); !errors.Is(err, ErrInvalidOAuthState) {
		t.Fatalf("expected ErrInvalidOAuthState for empty state, got %v", err)
	}
}

func TestOAuthStateTokenRejectsExpiry(t *testing.T) {
	manager := testTokenManager()
	// Issue the token 11 minutes in the past so its 10-minute TTL has lapsed
	// by wall clock (the JWT parser validates expiry against real time).
	manager.now = func() time.Time { return time.Now().Add(-11 * time.Minute) }
	raw, err := manager.NewOAuthStateToken("github", "/app/settings")
	if err != nil {
		t.Fatalf("issue state token: %v", err)
	}
	if _, err := manager.ParseOAuthStateToken(raw); !errors.Is(err, ErrInvalidOAuthState) {
		t.Fatalf("expected ErrInvalidOAuthState for expired state, got %v", err)
	}
}

func TestOAuthStateTokenRejectsRegistrationToken(t *testing.T) {
	manager := testTokenManager()
	raw, err := manager.NewGoogleRegistrationToken("sub-1", "alice@example.com", "Alice")
	if err != nil {
		t.Fatalf("issue registration token: %v", err)
	}
	if _, err := manager.ParseOAuthStateToken(raw); !errors.Is(err, ErrInvalidOAuthState) {
		t.Fatalf("registration token must not pass as state, got %v", err)
	}
}

func TestSanitizeOAuthNext(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", defaultOAuthNext},
		{"   ", defaultOAuthNext},
		{"//evil.example.com/app/x", defaultOAuthNext},
		{"https://evil.example.com/app/x", defaultOAuthNext},
		{"/evil", defaultOAuthNext},
		{"/app", defaultOAuthNext},
		{"/app/x", "/app/x"},
		{" /app/settings ", "/app/settings"},
	}
	for _, tc := range cases {
		if got := sanitizeOAuthNext(tc.in); got != tc.want {
			t.Errorf("sanitizeOAuthNext(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	raw, err := testTokenManager().NewOAuthStateToken("github", "https://evil.example.com/app/x")
	if err != nil {
		t.Fatalf("issue state token: %v", err)
	}
	claims, err := testTokenManager().ParseOAuthStateToken(raw)
	if err != nil {
		t.Fatalf("parse state token: %v", err)
	}
	if claims.Next != defaultOAuthNext {
		t.Fatalf("unsafe next leaked into state token: %q", claims.Next)
	}
}

func TestOAuthRegistrationTokenCarriesProvider(t *testing.T) {
	manager := testTokenManager()

	google, err := manager.NewGoogleRegistrationToken("sub-google", "alice@example.com", "Alice")
	if err != nil {
		t.Fatalf("issue google registration token: %v", err)
	}
	claims, err := manager.ParseGoogleRegistrationToken(google)
	if err != nil {
		t.Fatalf("parse google registration token: %v", err)
	}
	if claims.Provider != "google" || claims.Subject != "sub-google" {
		t.Fatalf("unexpected google registration claims: %+v", claims)
	}

	for _, provider := range []string{"github", "yandex"} {
		phone := ""
		if provider == "yandex" {
			phone = "+79001234567"
		}
		raw, err := manager.NewOAuthRegistrationToken(provider, "sub-"+provider, "a@example.com", "A", phone)
		if err != nil {
			t.Fatalf("issue %s registration token: %v", provider, err)
		}
		claims, err := manager.ParseGoogleRegistrationToken(raw)
		if err != nil {
			t.Fatalf("parse %s registration token: %v", provider, err)
		}
		if claims.Provider != provider || claims.Subject != "sub-"+provider || claims.Purpose != GoogleRegistrationPurpose {
			t.Fatalf("unexpected %s registration claims: %+v", provider, claims)
		}
		if claims.Phone != phone {
			t.Fatalf("unexpected %s registration phone: %+v", provider, claims)
		}
	}
}

func TestRegistrationTokenRejectsStateToken(t *testing.T) {
	manager := testTokenManager()
	raw, err := manager.NewOAuthStateToken("github", "/app/settings")
	if err != nil {
		t.Fatalf("issue state token: %v", err)
	}
	if _, err := manager.ParseGoogleRegistrationToken(raw); err == nil {
		t.Fatal("state token must not pass as a registration token")
	}
}
