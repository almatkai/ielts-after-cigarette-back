package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth/oauth"
	"github.com/go-chi/chi/v5"
)

type fakeOAuthProvider struct {
	name      string
	identity  oauth.ExternalIdentity
	err       error
	lastState string
	lastCode  string
}

func (f *fakeOAuthProvider) Name() string { return f.name }

func (f *fakeOAuthProvider) StartURL(state string) string {
	f.lastState = state
	return "https://provider.example/authorize?state=" + url.QueryEscape(state)
}

func (f *fakeOAuthProvider) Exchange(_ context.Context, code string) (oauth.ExternalIdentity, error) {
	f.lastCode = code
	return f.identity, f.err
}

type oauthHandlerFixture struct {
	router   *chi.Mux
	service  *Service
	github   *fakeOAuthProvider
	yandex   *fakeOAuthProvider
	frontend string
}

func newOAuthHandlerFixture(t *testing.T) *oauthHandlerFixture {
	t.Helper()
	service, _ := testGoogleService("admin@example.com")
	github := &fakeOAuthProvider{name: "github"}
	yandex := &fakeOAuthProvider{name: "yandex"}
	handler := NewOAuthHandler(
		service,
		service.tokens,
		map[string]oauth.Provider{"github": github, "yandex": yandex},
		"https://app.example.com",
		CookieConfig{Name: "ielts_refresh", SameSite: http.SameSiteLaxMode, MaxAge: time.Hour},
		1<<20,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	router := chi.NewRouter()
	router.Get("/api/v1/auth/{provider}/start", handler.Start)
	router.Get("/api/v1/auth/{provider}/callback", handler.Callback)
	return &oauthHandlerFixture{
		router:   router,
		service:  service,
		github:   github,
		yandex:   yandex,
		frontend: "https://app.example.com",
	}
}

func (f *oauthHandlerFixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}

func oauthRedirect(t *testing.T, response *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	if response.Code != http.StatusFound {
		t.Fatalf("expected a redirect, got %d: %s", response.Code, response.Body.String())
	}
	target, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	return target
}

func TestOAuthStartRedirectsToProviderAuthorizePage(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	response := fixture.get(t, "/api/v1/auth/github/start?next=/app/settings")
	target := oauthRedirect(t, response)

	if target.Host != "provider.example" {
		t.Fatalf("expected the provider authorize page, got %q", target)
	}
	state := target.Query().Get("state")
	if state == "" || state != fixture.github.lastState {
		t.Fatal("start URL does not carry the issued state token")
	}
	claims, err := fixture.service.tokens.ParseOAuthStateToken(state)
	if err != nil {
		t.Fatalf("state token did not verify: %v", err)
	}
	if claims.Provider != "github" {
		t.Fatalf("state is bound to %q, expected github", claims.Provider)
	}
	if claims.Next != "/app/settings" {
		t.Fatalf("unexpected next claim %q", claims.Next)
	}
}

func TestOAuthStartSanitizesNext(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	fixture.get(t, "/api/v1/auth/github/start?next=https://evil.example.com/app/x")
	claims, err := fixture.service.tokens.ParseOAuthStateToken(fixture.github.lastState)
	if err != nil {
		t.Fatalf("state token did not verify: %v", err)
	}
	if claims.Next != "/app/dashboard" {
		t.Fatalf("unsafe next must fall back to the dashboard, got %q", claims.Next)
	}
}

func TestOAuthStartRejectsUnknownProvider(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	response := fixture.get(t, "/api/v1/auth/vk/start")
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", response.Code)
	}
	var payload struct {
		Code string `json:"code"`
	}
	decodeBody(t, response, &payload)
	if payload.Code != "NOT_FOUND" {
		t.Fatalf("unexpected error code: %q", payload.Code)
	}
}

func TestOAuthCallbackCreatesSessionAndReturnsToNext(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	if _, _, err := fixture.service.Register(context.Background(), RegisterInput{
		Name: "Alice", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	fixture.github.identity = oauth.ExternalIdentity{Sub: "gh-alice", Email: "alice@example.com", Name: "Alice"}

	state, err := fixture.service.tokens.NewOAuthStateToken("github", "/app/settings")
	if err != nil {
		t.Fatalf("mint state: %v", err)
	}
	response := fixture.get(t, "/api/v1/auth/github/callback?state="+url.QueryEscape(state)+"&code=the-code")

	target := oauthRedirect(t, response)
	if target.String() != "https://app.example.com/app/settings" {
		t.Fatalf("expected redirect into the app, got %q", target.String())
	}
	if fixture.github.lastCode != "the-code" {
		t.Fatalf("provider did not receive the code, got %q", fixture.github.lastCode)
	}
	var refresh *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "ielts_refresh" {
			refresh = cookie
		}
	}
	if refresh == nil || refresh.Value == "" {
		t.Fatal("expected a refresh session cookie")
	}
	if !refresh.HttpOnly || refresh.Path != "/api/v1/auth" {
		t.Fatalf("unexpected cookie attributes: %+v", refresh)
	}
}

func TestOAuthCallbackRedirectsPendingRegistration(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	fixture.github.identity = oauth.ExternalIdentity{Sub: "gh-new", Email: "newcomer@example.com", Name: "New Person"}

	state, err := fixture.service.tokens.NewOAuthStateToken("github", "/app/settings")
	if err != nil {
		t.Fatalf("mint state: %v", err)
	}
	response := fixture.get(t, "/api/v1/auth/github/callback?state="+url.QueryEscape(state)+"&code=the-code")

	target := oauthRedirect(t, response)
	if target.Scheme+"://"+target.Host+target.Path != "https://app.example.com/app/login" {
		t.Fatalf("expected the frontend login page, got %q", target)
	}
	token := target.Query().Get("registration")
	if token == "" {
		t.Fatalf("expected a registration token in %q", target)
	}
	claims, err := fixture.service.tokens.ParseGoogleRegistrationToken(token)
	if err != nil {
		t.Fatalf("registration token did not verify: %v", err)
	}
	if claims.Provider != "github" || claims.Subject != "gh-new" {
		t.Fatalf("unexpected registration claims: %+v", claims)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "ielts_refresh" {
			t.Fatal("pending registration must not set a session cookie")
		}
	}
}

func TestOAuthCallbackPropagatesProviderError(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	// The provider error short-circuits before state validation.
	response := fixture.get(t, "/api/v1/auth/github/callback?error=access_denied")
	target := oauthRedirect(t, response)
	if target.Query().Get("oauth_error") != "access_denied" {
		t.Fatalf("expected oauth_error=access_denied, got %q", target)
	}
}

func TestOAuthCallbackRejectsInvalidState(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)

	response := fixture.get(t, "/api/v1/auth/github/callback?state=garbage&code=the-code")
	if target := oauthRedirect(t, response); target.Query().Get("oauth_error") != "invalid_state" {
		t.Fatalf("expected oauth_error=invalid_state, got %q", target)
	}

	// A state minted for another provider is equally invalid on this route.
	state, err := fixture.service.tokens.NewOAuthStateToken("yandex", "/app/settings")
	if err != nil {
		t.Fatalf("mint state: %v", err)
	}
	response = fixture.get(t, "/api/v1/auth/github/callback?state="+url.QueryEscape(state)+"&code=the-code")
	if target := oauthRedirect(t, response); target.Query().Get("oauth_error") != "invalid_state" {
		t.Fatalf("cross-provider state must be rejected, got %q", target)
	}
}

func TestOAuthCallbackRequiresCode(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	state, err := fixture.service.tokens.NewOAuthStateToken("github", "/app/settings")
	if err != nil {
		t.Fatalf("mint state: %v", err)
	}
	response := fixture.get(t, "/api/v1/auth/github/callback?state="+url.QueryEscape(state))
	if target := oauthRedirect(t, response); target.Query().Get("oauth_error") != "access_denied" {
		t.Fatalf("expected oauth_error=access_denied, got %q", target)
	}
}

func TestOAuthCallbackMapsExchangeFailures(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	mint := func(t *testing.T) string {
		t.Helper()
		state, err := fixture.service.tokens.NewOAuthStateToken("github", "/app/settings")
		if err != nil {
			t.Fatalf("mint state: %v", err)
		}
		return url.QueryEscape(state)
	}

	fixture.github.err = oauth.ErrEmailRequired
	response := fixture.get(t, "/api/v1/auth/github/callback?state="+mint(t)+"&code=the-code")
	if target := oauthRedirect(t, response); target.Query().Get("oauth_error") != "email_required" {
		t.Fatalf("expected oauth_error=email_required, got %q", target)
	}

	fixture.github.err = errors.New("provider exploded")
	response = fixture.get(t, "/api/v1/auth/github/callback?state="+mint(t)+"&code=the-code")
	if target := oauthRedirect(t, response); target.Query().Get("oauth_error") != "exchange_failed" {
		t.Fatalf("expected oauth_error=exchange_failed, got %q", target)
	}
}

func TestOAuthCallbackWorksForYandexToo(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	fixture.yandex.identity = oauth.ExternalIdentity{Sub: "ya-admin", Email: "admin@example.com", Name: "Admin"}

	state, err := fixture.service.tokens.NewOAuthStateToken("yandex", "/app/admin")
	if err != nil {
		t.Fatalf("mint state: %v", err)
	}
	response := fixture.get(t, "/api/v1/auth/yandex/callback?state="+url.QueryEscape(state)+"&code=the-code")

	target := oauthRedirect(t, response)
	if !strings.HasPrefix(target.String(), "https://app.example.com/app/admin") {
		t.Fatalf("expected redirect into the app, got %q", target.String())
	}
}

func TestOAuthCallbackRejectsUnknownProvider(t *testing.T) {
	fixture := newOAuthHandlerFixture(t)
	response := fixture.get(t, "/api/v1/auth/vk/callback?state=x&code=y")
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", response.Code)
	}
}
