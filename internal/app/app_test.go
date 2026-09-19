package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsMediaUpload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/api/v1/admin/listening/media", true},
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
	handler := timeoutByRequest(time.Second, 5*time.Minute)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
