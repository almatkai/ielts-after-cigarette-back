package mcpkeys

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestHandler_Create_List_Revoke(t *testing.T) {
	repo := newMockRepo()
	adminID := uuid.New()
	userFinder := &mockUserFinder{
		users: map[uuid.UUID]auth.UserView{
			adminID: {
				ID:   adminID,
				Role: auth.RoleAdmin,
			},
		},
	}
	service := NewService(repo, userFinder)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger, 1<<20)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUser(req.Context(), adminID, auth.RoleAdmin)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/admin/mcp-keys", handler.List)
	r.Post("/admin/mcp-keys", handler.Create)
	r.Delete("/admin/mcp-keys/{keyID}", handler.Revoke)

	// 1. Create key
	body, _ := json.Marshal(CreateKeyInput{Name: "Antigravity Agent"})
	req := httptest.NewRequest(http.MethodPost, "/admin/mcp-keys", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var createResp CreateKeyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if createResp.Name != "Antigravity Agent" {
		t.Errorf("expected name 'Antigravity Agent', got %s", createResp.Name)
	}
	if createResp.SecretKey == "" {
		t.Errorf("expected non-empty SecretKey")
	}

	// 2. List keys
	req = httptest.NewRequest(http.MethodGet, "/admin/mcp-keys", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", w.Code)
	}

	var listResp ListKeysResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(listResp.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(listResp.Keys))
	}

	// 3. Revoke key
	req = httptest.NewRequest(http.MethodDelete, "/admin/mcp-keys/"+createResp.ID.String(), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 No Content, got %d", w.Code)
	}

	// 4. Revoke again returns 404
	req = httptest.NewRequest(http.MethodDelete, "/admin/mcp-keys/"+createResp.ID.String(), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d", w.Code)
	}
}
