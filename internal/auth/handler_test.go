package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
