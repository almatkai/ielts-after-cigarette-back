package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadyReportsDependencyFailure(t *testing.T) {
	handler := NewHandler(
		func(context.Context) error { return errors.New("database down") },
		func(context.Context) error { return nil },
	)
	response := httptest.NewRecorder()
	handler.Ready(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d", http.StatusServiceUnavailable, response.Code)
	}
	if body := response.Body.String(); body == "" || !contains(body, `"postgres":"unavailable"`) {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
