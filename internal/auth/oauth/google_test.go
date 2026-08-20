package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/waitlist"
)

// fakeGoogleVerifier records the verified token and returns canned claims.
type fakeGoogleVerifier struct {
	claims   waitlist.GoogleClaims
	err      error
	gotToken string
}

func (f *fakeGoogleVerifier) Verify(_ context.Context, idToken string) (waitlist.GoogleClaims, error) {
	f.gotToken = idToken
	return f.claims, f.err
}

func newTestGoogle(server *httptest.Server, verifier waitlist.GoogleTokenVerifier) *Google {
	provider := NewGoogle("cid", "secret", "http://localhost/callback", verifier, server.Client())
	provider.authorizeURL = server.URL + "/authorize"
	provider.tokenURL = server.URL + "/token"
	return provider
}

func TestGoogleStartURLCarriesClientAndState(t *testing.T) {
	provider := NewGoogle("cid", "secret", "http://localhost/callback", nil, nil)
	start := provider.StartURL("state-token")
	for _, fragment := range []string{
		googleAuthorizeURL,
		"response_type=code",
		"client_id=cid",
		"redirect_uri=http%3A%2F%2Flocalhost%2Fcallback",
		"scope=openid+email+profile",
		"state=state-token",
	} {
		if !strings.Contains(start, fragment) {
			t.Errorf("start URL %q is missing %q", start, fragment)
		}
	}
	if provider.Name() != "google" {
		t.Fatalf("unexpected provider name %q", provider.Name())
	}
}

// googleServer serves a token endpoint that asserts the grant form and
// replies with the given status and payload.
func googleServer(t *testing.T, status int, payload map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("unexpected grant_type %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "secret" {
			t.Errorf("token request lost credentials: %v", r.Form)
		}
		if r.Form.Get("redirect_uri") != "http://localhost/callback" {
			t.Errorf("unexpected redirect_uri %q", r.Form.Get("redirect_uri"))
		}
		if r.Form.Get("code") != "the-code" {
			t.Errorf("unexpected code %q", r.Form.Get("code"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(payload)
	}))
}

func TestGoogleExchangeReturnsVerifiedIdentity(t *testing.T) {
	server := googleServer(t, http.StatusOK, map[string]any{
		"id_token":     "the-id-token",
		"access_token": "ignored",
	})
	defer server.Close()

	verifier := &fakeGoogleVerifier{claims: waitlist.GoogleClaims{
		Sub:           "ggl-sub-1",
		Email:         "alice@example.com",
		EmailVerified: true,
		Name:          "Alice",
	}}
	identity, err := newTestGoogle(server, verifier).Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if verifier.gotToken != "the-id-token" {
		t.Fatalf("verifier received %q, want the id token from the token response", verifier.gotToken)
	}
	want := ExternalIdentity{Sub: "ggl-sub-1", Email: "alice@example.com", Name: "Alice"}
	if identity != want {
		t.Fatalf("unexpected identity: got %+v want %+v", identity, want)
	}
}

func TestGoogleExchangeRequiresVerifiedEmail(t *testing.T) {
	server := googleServer(t, http.StatusOK, map[string]any{"id_token": "the-id-token"})
	defer server.Close()

	for name, claims := range map[string]waitlist.GoogleClaims{
		"unverified": {Sub: "sub", Email: "alice@example.com", EmailVerified: false},
		"empty":      {Sub: "sub", Email: "", EmailVerified: true},
	} {
		t.Run(name, func(t *testing.T) {
			verifier := &fakeGoogleVerifier{claims: claims}
			_, err := newTestGoogle(server, verifier).Exchange(context.Background(), "the-code")
			if !errors.Is(err, ErrEmailRequired) {
				t.Fatalf("expected ErrEmailRequired, got %v", err)
			}
		})
	}
}

func TestGoogleExchangePropagatesTokenAPIError(t *testing.T) {
	server := googleServer(t, http.StatusBadRequest, map[string]any{
		"error":             "invalid_grant",
		"error_description": "Malformed auth code.",
	})
	defer server.Close()

	_, err := newTestGoogle(server, &fakeGoogleVerifier{}).Exchange(context.Background(), "the-code")
	if err == nil || !strings.Contains(err.Error(), "Malformed auth code.") {
		t.Fatalf("expected the provider error description, got %v", err)
	}
	if errors.Is(err, ErrEmailRequired) {
		t.Fatal("token exchange failures must not surface as ErrEmailRequired")
	}
}

func TestGoogleExchangeRequiresIDToken(t *testing.T) {
	server := googleServer(t, http.StatusOK, map[string]any{"access_token": "no-id-token"})
	defer server.Close()

	_, err := newTestGoogle(server, &fakeGoogleVerifier{}).Exchange(context.Background(), "the-code")
	if err == nil || !strings.Contains(err.Error(), "no id token") {
		t.Fatalf("expected a missing id token error, got %v", err)
	}
}

func TestGoogleExchangePropagatesVerifierFailure(t *testing.T) {
	server := googleServer(t, http.StatusOK, map[string]any{"id_token": "the-id-token"})
	defer server.Close()

	verifier := &fakeGoogleVerifier{err: errors.New("audience mismatch")}
	_, err := newTestGoogle(server, verifier).Exchange(context.Background(), "the-code")
	if err == nil || !strings.Contains(err.Error(), "audience mismatch") {
		t.Fatalf("expected the verifier error, got %v", err)
	}
}

func TestGoogleExchangeRequiresVerifier(t *testing.T) {
	server := googleServer(t, http.StatusOK, map[string]any{"id_token": "the-id-token"})
	defer server.Close()

	_, err := newTestGoogle(server, nil).Exchange(context.Background(), "the-code")
	if err == nil || !strings.Contains(err.Error(), "verifier is not configured") {
		t.Fatalf("expected a missing verifier error, got %v", err)
	}
}
