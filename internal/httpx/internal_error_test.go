package httpx

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalErrorReportsCauseAndActorButKeepsResponseSafe(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(WithActor(r.Context(), "internal-user-id"))
		InternalError(w, r, logger, errors.New("private database failure"))
	}))
	r := httptest.NewRequest("GET", "/api/v1/dashboard", nil)
	r.Header.Set("X-Request-ID", "support-id")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"requestId":"support-id"`) || strings.Contains(w.Body.String(), "private database failure") {
		t.Fatalf("unsafe or uncorrelated response: %s", w.Body.String())
	}
	for _, value := range []string{"support-id", "internal-user-id", "private database failure"} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("missing %q from logs", value)
		}
	}
}

func TestCORSExposesRequestID(t *testing.T) {
	h := CORS([]string{"https://app.example"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Header().Get("Access-Control-Expose-Headers"), "X-Request-ID") {
		t.Fatal("frontend cannot read request ID")
	}
}
