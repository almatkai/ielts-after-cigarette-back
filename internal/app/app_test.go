package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/go-chi/chi/v5"
)

func TestOnlyGoogleAuthEntryPointsArePublic(t *testing.T) {
	router := New(config.Config{JWTSecret: "test-secret-at-least-32-characters-long"}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	routes := map[string]bool{}
	if err := chi.Walk(router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/login"} {
		if routes["POST "+path] {
			t.Fatalf("password endpoint %s must not be publicly reachable", path)
		}
	}
	for _, path := range []string{"/api/v1/auth/google", "/api/v1/auth/google/complete"} {
		if !routes["POST "+path] {
			t.Fatalf("Google endpoint %s is missing", path)
		}
	}
}

func TestGuestEntryIsDisabledByDefaultAndChatRequiresAccount(t *testing.T) {
	router := New(config.Config{JWTSecret: "test-secret-at-least-32-characters-long"}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{"/api/v1/assistant/chat", "/api/v1/assistant/chat/stream"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous chat accessible: %s %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/guest/start", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("guest launch enabled by default: %d", w.Code)
	}
}

func TestFullMocksCannotBeSelectedOrManuallyCreated(t *testing.T) {
	router := New(config.Config{JWTSecret: "test-secret-at-least-32-characters-long"}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	routes := map[string]bool{}
	if err := chi.Walk(router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"GET /api/v1/full-mocks", "GET /api/v1/full-mocks/{mockID}",
		"POST /api/v1/full-mocks/{mockID}/sessions", "POST /api/v1/admin/full-mocks",
		"PUT /api/v1/admin/full-mocks/{mockID}", "POST /api/v1/admin/full-mocks/{mockID}/publish",
	} {
		if routes[route] {
			t.Fatalf("manual mock route is still reachable: %s", route)
		}
	}
	for _, methodPath := range []struct{ method, path string }{
		{"GET", "/api/v1/full-mocks/overview"}, {"POST", "/api/v1/full-mocks/start"},
	} {
		if !routes[methodPath.method+" "+methodPath.path] {
			t.Fatalf("generated mock route missing: %+v", methodPath)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(methodPath.method, methodPath.path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected mock endpoint: %d", response.Code)
		}
	}
}

func TestIsMediaUpload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/api/v1/admin/listening/media", true},
		{http.MethodPost, "/api/v1/admin/writing/media", true},
		{http.MethodPost, "/api/v1/attempts/abc/recordings", true},
		{http.MethodGet, "/api/v1/admin/listening/media", false},
		{http.MethodPost, "/api/v1/admin/listening/tests", false},
		{http.MethodPost, "/api/v1/attempts/abc/submit", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, tt.path, nil)
		if got := isMediaUpload(r); got != tt.want {
			t.Errorf("isMediaUpload(%s %s) = %v, want %v", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestTimeoutByRequestUsesMediaDeadline(t *testing.T) {
	t.Parallel()

	var deadline time.Time
	handler := timeoutByRequest(time.Second, 5*time.Minute, 2*time.Minute)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, _ = r.Context().Deadline()
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/listening/media", nil)
	started := time.Now()
	handler.ServeHTTP(httptest.NewRecorder(), request)

	remaining := deadline.Sub(started)
	if remaining < 4*time.Minute || remaining > 5*time.Minute+time.Second {
		t.Fatalf("media upload deadline remaining = %s, want approximately 5m", remaining)
	}
}

func TestTimeoutByRequestUsesAIEvaluationDeadline(t *testing.T) {
	t.Parallel()

	var deadline time.Time
	handler := timeoutByRequest(time.Second, 5*time.Minute, 2*time.Minute)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, _ = r.Context().Deadline()
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/attempts/abc/submit", nil)
	started := time.Now()
	handler.ServeHTTP(httptest.NewRecorder(), request)

	remaining := deadline.Sub(started)
	if remaining < 90*time.Second || remaining > 2*time.Minute+time.Second {
		t.Fatalf("AI evaluation deadline remaining = %s, want approximately 2m", remaining)
	}
}

func TestRemoteIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{
			name:       "trusts X-Forwarded-For from private proxy peer",
			remoteAddr: "172.18.0.2:5432",
			forwarded:  "203.0.113.5, 10.0.0.1",
			want:       "203.0.113.5",
		},
		{
			name:       "trusts X-Forwarded-For from loopback peer",
			remoteAddr: "127.0.0.1:8080",
			forwarded:  "198.51.100.9",
			want:       "198.51.100.9",
		},
		{
			name:       "falls back to peer when header missing",
			remoteAddr: "172.18.0.2:5432",
			want:       "172.18.0.2",
		},
		{
			name:       "ignores spoofed header from public peer",
			remoteAddr: "203.0.113.99:1234",
			forwarded:  "198.51.100.9",
			want:       "203.0.113.99",
		},
		{
			name:       "handles remote addr without port",
			remoteAddr: "192.168.1.10",
			forwarded:  "198.51.100.9",
			want:       "198.51.100.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := remoteIP(r); got != tt.want {
				t.Fatalf("remoteIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
