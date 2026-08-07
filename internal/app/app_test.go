package app

import (
	"net/http/httptest"
	"testing"
)

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
