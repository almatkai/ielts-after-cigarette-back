package attempts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Only GetStatus is implemented: any heavy read by the polling handler panics.
type statusOnlyRepository struct {
	Repository
	owner   uuid.UUID
	attempt uuid.UUID
}

func (r statusOnlyRepository) GetStatus(_ context.Context, owner, id uuid.UUID) (StatusDetail, error) {
	if owner != r.owner || id != r.attempt {
		return StatusDetail{}, ErrNotFound
	}
	return StatusDetail{ID: id, Status: StatusProcessing, WritingAssessment: &WritingAssessmentJob{Status: "QUEUED"}}, nil
}

func TestStatusHandlerIsSmallAndAuthenticated(t *testing.T) {
	owner, attempt := uuid.New(), uuid.New()
	service := NewService(statusOnlyRepository{owner: owner, attempt: attempt}, nil)
	handler := NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1024)
	tokens := auth.NewTokenManager("test-secret", "test", "test", time.Hour, time.Hour)
	router := chi.NewRouter()
	router.Use(auth.Authenticate(tokens))
	router.Get("/attempts/{attemptID}/status", handler.Status)
	for _, tc := range []struct {
		name string
		user uuid.UUID
		id   string
		want int
	}{
		{"owner", owner, attempt.String(), http.StatusOK},
		{"foreign", uuid.New(), attempt.String(), http.StatusNotFound},
		{"missing", owner, uuid.New().String(), http.StatusNotFound},
		{"invalid ID", owner, "not-a-uuid", http.StatusBadRequest},
		{"anonymous", uuid.Nil, attempt.String(), http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/attempts/"+tc.id+"/status", nil)
			if tc.user != uuid.Nil {
				token, _, err := tokens.NewAccessToken(tc.user, auth.RoleStudent)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.want, response.Body)
			}
			if tc.want != http.StatusOK {
				return
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("status snapshots must not be cached by the browser")
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 3 || body["id"] == nil || body["status"] == nil || body["writingAssessment"] == nil {
				t.Fatalf("unexpected status payload: %s", response.Body)
			}
		})
	}
}
