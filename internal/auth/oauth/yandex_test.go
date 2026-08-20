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

func newTestYandex(server *httptest.Server) *Yandex {
	provider := NewYandex("cid", "secret", "http://localhost/callback", server.Client())
	provider.authorizeURL = server.URL + "/authorize"
	provider.tokenURL = server.URL + "/token"
	provider.infoURL = server.URL + "/info"
	return provider
}

func TestYandexStartURLCarriesClientAndState(t *testing.T) {
	provider := NewYandex("cid", "secret", "http://localhost/callback", nil)
	start := provider.StartURL("state-token")
	for _, fragment := range []string{
		yandexAuthorizeURL,
		"response_type=code",
		"client_id=cid",
		"redirect_uri=http%3A%2F%2Flocalhost%2Fcallback",
		"state=state-token",
	} {
		if !strings.Contains(start, fragment) {
			t.Errorf("start URL %q is missing %q", start, fragment)
		}
	}
	if provider.Name() != "yandex" {
		t.Fatalf("unexpected provider name %q", provider.Name())
	}
}

// yandexServer serves a token endpoint that asserts the grant form and an
// /info endpoint returning the given profile payload.
func yandexServer(t *testing.T, info map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
			}
			if r.Form.Get("grant_type") != "authorization_code" {
				t.Errorf("unexpected grant_type %q", r.Form.Get("grant_type"))
			}
			if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "secret" {
				t.Errorf("token request lost credentials: %v", r.Form)
			}
			if r.Form.Get("code") != "the-code" {
				t.Errorf("unexpected code %q", r.Form.Get("code"))
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "ya-token"})
		case "/info":
			if got := r.Header.Get("Authorization"); got != "OAuth ya-token" {
				t.Errorf("info request authorization = %q", got)
			}
			json.NewEncoder(w).Encode(info)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestYandexExchangeUsesDefaultEmail(t *testing.T) {
	server := yandexServer(t, map[string]any{
		"id": "42", "login": "yauser", "display_name": "Ya User",
		"default_email": "yauser@yandex.ru", "emails": []string{"yauser@yandex.ru"},
		"default_phone": map[string]any{"id": 1, "phone": "+79001234567"},
	})
	defer server.Close()

	identity, err := newTestYandex(server).Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	want := ExternalIdentity{Sub: "42", Email: "yauser@yandex.ru", Name: "Ya User", Phone: "+79001234567"}
	if identity != want {
		t.Fatalf("unexpected identity: got %+v want %+v", identity, want)
	}
}

func TestYandexExchangeFallsBackToEmailsListAndRealName(t *testing.T) {
	server := yandexServer(t, map[string]any{
		"id": "7", "login": "fallback", "real_name": "Fallback Human",
		"emails": []string{"first@ya.ru", "second@ya.ru"},
	})
	defer server.Close()

	identity, err := newTestYandex(server).Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if identity.Email != "first@ya.ru" {
		t.Fatalf("expected the first listed email, got %q", identity.Email)
	}
	if identity.Name != "Fallback Human" {
		t.Fatalf("expected the real name, got %q", identity.Name)
	}
}

func TestYandexExchangeFallsBackToLoginForName(t *testing.T) {
	server := yandexServer(t, map[string]any{
		"id": "9", "login": "justlogin", "default_email": "justlogin@ya.ru",
	})
	defer server.Close()

	identity, err := newTestYandex(server).Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if identity.Name != "justlogin" {
		t.Fatalf("expected the login as name, got %q", identity.Name)
	}
}

func TestYandexExchangeRequiresEmail(t *testing.T) {
	server := yandexServer(t, map[string]any{"id": "5", "login": "nomail"})
	defer server.Close()

	_, err := newTestYandex(server).Exchange(context.Background(), "the-code")
	if !errors.Is(err, ErrEmailRequired) {
		t.Fatalf("expected ErrEmailRequired, got %v", err)
	}
}

func TestYandexExchangeSurfacesTokenErrorDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "Code has expired",
		})
	}))
	defer server.Close()

	_, err := newTestYandex(server).Exchange(context.Background(), "stale-code")
	if err == nil || !strings.Contains(err.Error(), "Code has expired") {
		t.Fatalf("expected the provider error description, got %v", err)
	}
	if errors.Is(err, ErrEmailRequired) {
		t.Fatal("token failure must not masquerade as a missing email")
	}
}
