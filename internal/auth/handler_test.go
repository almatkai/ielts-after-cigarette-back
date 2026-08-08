package auth

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoogleLoginRespondsWithSessionShape(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	handler := testAuthHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", strings.NewReader(`{"googleToken":"admin"}`))
	response := httptest.NewRecorder()
	handler.GoogleLogin(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	var payload map[string]any
	decodeBody(t, response, &payload)
	for _, key := range []string{"accessToken", "tokenType", "expiresIn", "user"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("session response missing %q: %v", key, payload)
		}
	}
	for _, key := range []string{"registrationRequired", "registrationToken", "profile", "refreshToken"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("session response must not contain %q: %v", key, payload)
		}
	}
	if len(response.Result().Cookies()) != 1 {
		t.Fatalf("expected refresh cookie, got %v", response.Result().Cookies())
	}
}

func TestGoogleLoginRespondsWithPendingRegistrationShape(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	handler := testAuthHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", strings.NewReader(`{"googleToken":"person"}`))
	response := httptest.NewRecorder()
	handler.GoogleLogin(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	var payload map[string]any
	decodeBody(t, response, &payload)
	if payload["registrationRequired"] != true || payload["registrationToken"] == "" || payload["registrationToken"] == nil {
		t.Fatalf("unexpected pending registration payload: %v", payload)
	}
	profile, ok := payload["profile"].(map[string]any)
	if !ok || profile["email"] != "person@example.com" || profile["name"] != "Test User" {
		t.Fatalf("unexpected profile: %v", payload["profile"])
	}
	for _, key := range []string{"accessToken", "refreshToken", "user"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("pending registration must not contain %q: %v", key, payload)
		}
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatalf("pending registration must not set a cookie: %v", response.Result().Cookies())
	}
}

func TestCompleteGoogleRegistrationReturnsCreatedSession(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	pending, err := service.GoogleLogin(t.Context(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil || pending.PendingRegistration == nil {
		t.Fatalf("expected pending registration, got %+v err=%v", pending, err)
	}
	handler := testAuthHandler(service)
	body := `{"registrationToken":"` + pending.PendingRegistration.Token + `","name":"Person Personov","phone":"+7 700 123 45 67","password":"safe-password","acceptedTerms":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google/complete", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.CompleteGoogleRegistration(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	decodeBody(t, response, &payload)
	for _, key := range []string{"accessToken", "tokenType", "expiresIn", "user"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("complete response missing %q: %v", key, payload)
		}
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "ielts_refresh" || cookies[0].Value == "" {
		t.Fatalf("expected refresh cookie, got %v", cookies)
	}
}

func TestCompleteGoogleRegistrationRejectsBadTokenWithUnauthorized(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	handler := testAuthHandler(service)
	body := `{"registrationToken":"bogus","name":"Person","phone":"+77001234567","password":"safe-password","acceptedTerms":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google/complete", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.CompleteGoogleRegistration(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	decodeBody(t, response, &payload)
	if payload.Code != "GOOGLE_TOKEN_INVALID" {
		t.Fatalf("unexpected error code: %q", payload.Code)
	}
}

func testAuthHandler(service *Service) *Handler {
	return NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20, CookieConfig{
		Name:     "ielts_refresh",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * time.Hour,
	})
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}


func TestRefreshCookieAttributes(t *testing.T) {
	handler := &Handler{cookie: CookieConfig{
		Name:     "ielts_refresh",
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * time.Hour,
	}}
	response := httptest.NewRecorder()

	handler.setRefreshCookie(response, "opaque-token")

	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "ielts_refresh" || cookie.Value != "opaque-token" {
		t.Fatalf("unexpected cookie identity: %+v", cookie)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("missing secure cookie attributes: %+v", cookie)
	}
	if cookie.Path != "/api/v1/auth" || cookie.MaxAge <= 0 {
		t.Fatalf("unexpected cookie scope or lifetime: %+v", cookie)
	}
}

func TestClearRefreshCookieUsesMatchingScope(t *testing.T) {
	handler := &Handler{cookie: CookieConfig{
		Name:     "ielts_refresh",
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   time.Hour,
	}}
	response := httptest.NewRecorder()

	handler.clearRefreshCookie(response)

	cookie := response.Result().Cookies()[0]
	if cookie.Value != "" || cookie.MaxAge >= 0 {
		t.Fatalf("cookie was not expired: %+v", cookie)
	}
	if cookie.Path != "/api/v1/auth" || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("clear cookie scope does not match issued cookie: %+v", cookie)
	}
}
