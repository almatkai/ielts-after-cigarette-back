package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestGitHub points a GitHub provider at the test server, overriding the
// production endpoints while keeping the real HTTP plumbing.
func newTestGitHub(server *httptest.Server) *GitHub {
	provider := NewGitHub("cid", "secret", "http://localhost/callback", server.Client())
	provider.authorizeURL = server.URL + "/authorize"
	provider.tokenURL = server.URL + "/token"
	provider.userURL = server.URL + "/user"
	provider.emailsURL = server.URL + "/user/emails"
	return provider
}

func TestGitHubStartURLCarriesClientScopeAndState(t *testing.T) {
	provider := NewGitHub("cid", "secret", "http://localhost/callback", nil)
	start := provider.StartURL("state-token")
	for _, fragment := range []string{
		githubAuthorizeURL,
		"client_id=cid",
		"scope=read%3Auser+user%3Aemail",
		"redirect_uri=http%3A%2F%2Flocalhost%2Fcallback",
		"state=state-token",
	} {
		if !strings.Contains(start, fragment) {
			t.Errorf("start URL %q is missing %q", start, fragment)
		}
	}
	if provider.Name() != "github" {
		t.Fatalf("unexpected provider name %q", provider.Name())
	}
}

func TestGitHubExchangeFallsBackToPrimaryVerifiedEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if r.Method != http.MethodPost {
				t.Errorf("token endpoint method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
			}
			if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "secret" {
				t.Errorf("token request lost credentials: %v", r.Form)
			}
			if r.Form.Get("code") != "the-code" || r.Form.Get("redirect_uri") != "http://localhost/callback" {
				t.Errorf("unexpected token form: %v", r.Form)
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "gh-token"})
		case "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer gh-token" {
				t.Errorf("user request authorization = %q", got)
			}
			json.NewEncoder(w).Encode(map[string]any{"id": 12345, "login": "octocat"})
		case "/user/emails":
			json.NewEncoder(w).Encode([]map[string]any{
				{"email": "unverified@example.com", "primary": true, "verified": false},
				{"email": "octo@example.com", "primary": true, "verified": true},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	identity, err := newTestGitHub(server).Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if identity.Sub != "12345" {
		t.Fatalf("unexpected sub: %q", identity.Sub)
	}
	if identity.Email != "octo@example.com" {
		t.Fatalf("expected the primary verified email, got %q", identity.Email)
	}
	if identity.Name != "octocat" {
		t.Fatalf("expected name to fall back to the login, got %q", identity.Name)
	}
}

func TestGitHubExchangePrefersProfileEmailAndName(t *testing.T) {
	emailsCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			json.NewEncoder(w).Encode(map[string]string{"access_token": "gh-token"})
		case "/user":
			json.NewEncoder(w).Encode(map[string]any{
				"id": 7, "login": "octocat", "name": "The Octocat", "email": "public@example.com",
			})
		case "/user/emails":
			emailsCalled = true
			json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	identity, err := newTestGitHub(server).Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if identity.Email != "public@example.com" || identity.Name != "The Octocat" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if emailsCalled {
		t.Fatal("/user/emails must not be called when the profile email is present")
	}
}

func TestGitHubExchangeRequiresVerifiedEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			json.NewEncoder(w).Encode(map[string]string{"access_token": "gh-token"})
		case "/user":
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "login": "ghost"})
		case "/user/emails":
			json.NewEncoder(w).Encode([]map[string]any{
				{"email": "ghost@example.com", "primary": true, "verified": false},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := newTestGitHub(server).Exchange(context.Background(), "the-code")
	if !errors.Is(err, ErrEmailRequired) {
		t.Fatalf("expected ErrEmailRequired, got %v", err)
	}
}

func TestGitHubExchangeSurfacesTokenAPIFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "bad_verification_code",
			"error_description": "The code passed is incorrect or expired.",
		})
	}))
	defer server.Close()

	_, err := newTestGitHub(server).Exchange(context.Background(), "stale-code")
	if err == nil || !strings.Contains(err.Error(), "bad_verification_code") {
		t.Fatalf("expected the provider error to surface, got %v", err)
	}
	if errors.Is(err, ErrEmailRequired) {
		t.Fatal("token failure must not masquerade as a missing email")
	}
}

func TestGitHubExchangeRejectsNonOKTokenStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	if _, err := newTestGitHub(server).Exchange(context.Background(), "the-code"); err == nil {
		t.Fatal("expected an error for a 500 token endpoint")
	}
}
