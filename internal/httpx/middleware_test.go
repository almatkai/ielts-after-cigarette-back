package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSAllowsConfiguredCredentialedOrigin(t *testing.T) {
	handler := CORS([]string{"http://localhost:3000"})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	))
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/refresh", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected preflight success, got %d", response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("unexpected allowed origin: %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("credentialed CORS was not enabled")
	}
}

func TestCORSIdentityFormPostExceptionIsScoped(t *testing.T) {
	for _, test := range []struct {
		method, path, origin, contentType string
		status                            int
	}{
		{http.MethodPost, "/api/v1/auth/google", "https://accounts.google.com", "application/x-www-form-urlencoded", http.StatusNoContent},
		{http.MethodPost, "/api/v1/auth/refresh", "https://accounts.google.com", "application/x-www-form-urlencoded", http.StatusForbidden},
		{http.MethodPost, "/api/v1/auth/google", "https://attacker.example", "application/x-www-form-urlencoded", http.StatusForbidden},
		{http.MethodPost, "/api/v1/auth/google", "https://accounts.google.com", "application/json", http.StatusForbidden},
		{http.MethodGet, "/api/v1/auth/google", "https://accounts.google.com", "application/x-www-form-urlencoded", http.StatusForbidden},
	} {
		handler := CORS(nil, FormPostOrigin{Path: "/api/v1/auth/google", Origin: "https://accounts.google.com"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		request := httptest.NewRequest(test.method, test.path, strings.NewReader("credential=test"))
		request.Header.Set("Origin", test.origin)
		request.Header.Set("Content-Type", test.contentType)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s %s %s: expected %d, got %d", test.method, test.path, test.origin, test.contentType, test.status, response.Code)
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("form navigation exception must not grant cross-origin API reads")
		}
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	handler := CORS([]string{"http://localhost:3000"})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			t.Fatal("protected handler must not be called")
		},
	))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden origin, got %d", response.Code)
	}
}
