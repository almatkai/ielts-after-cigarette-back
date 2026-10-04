package app

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/observability"
)

func TestMetricsAreNotExposedOnPublicRouter(t *testing.T) {
	router := NewWithOptions(config.Config{JWTSecret: "test-secret-at-least-32-characters-long", RequestTimeout: time.Minute}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Metrics: observability.New(nil)})
	for _, path := range []string{"/metrics", "/api/v1/metrics"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("public metrics path %s is exposed: status=%d", path, w.Code)
		}
	}
}
