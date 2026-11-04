package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/mcpkeys"
	"github.com/google/uuid"
)

type mockAppMCPRepo struct {
	mu     sync.Mutex
	keys   map[uuid.UUID]mcpkeys.MCPKey
	hashes map[string]uuid.UUID
}

func (m *mockAppMCPRepo) Create(ctx context.Context, key mcpkeys.MCPKey, keyHash string) (*mcpkeys.MCPKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[key.ID] = key
	m.hashes[keyHash] = key.ID
	k := key
	return &k, nil
}

func (m *mockAppMCPRepo) List(ctx context.Context) ([]mcpkeys.MCPKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []mcpkeys.MCPKey
	for _, k := range m.keys {
		list = append(list, k)
	}
	return list, nil
}

func (m *mockAppMCPRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok || k.RevokedAt != nil {
		return mcpkeys.ErrKeyNotFound
	}
	now := time.Now().UTC()
	k.RevokedAt = &now
	m.keys[id] = k
	return nil
}

func (m *mockAppMCPRepo) FindByHash(ctx context.Context, keyHash string) (*mcpkeys.MCPKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.hashes[keyHash]
	if !ok {
		return nil, mcpkeys.ErrKeyNotFound
	}
	k := m.keys[id]
	if k.RevokedAt != nil {
		return nil, mcpkeys.ErrKeyRevoked
	}
	return &k, nil
}

func (m *mockAppMCPRepo) TouchLastUsed(ctx context.Context, id uuid.UUID, lastUsed time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return mcpkeys.ErrKeyNotFound
	}
	k.LastUsedAt = &lastUsed
	m.keys[id] = k
	return nil
}

func TestMCPKeyAuthenticationEndToEnd(t *testing.T) {
	adminID := uuid.New()
	mockRepo := &mockAppMCPRepo{
		keys:   make(map[uuid.UUID]mcpkeys.MCPKey),
		hashes: make(map[string]uuid.UUID),
	}
	mcpService := mcpkeys.NewService(mockRepo, nil)

	createdKey, err := mcpService.GenerateKey(context.Background(), "Integration Test Key", adminID)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	cfg := config.Config{
		JWTSecret:      "test-secret-at-least-32-characters-long",
		JWTIssuer:      "test",
		JWTAudience:    "test",
		AccessTokenTTL: time.Hour,
	}
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, time.Hour, time.Hour)
	tokens.WithAPIKeyValidator(mcpService)

	// Admin workspace router
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserID(r.Context())
		role := auth.Role(r.Context())
		hasPerm := auth.HasPermission(r.Context(), auth.PermissionContentEditor)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"userId":     userID.String(),
			"role":       role,
			"hasContent": hasPerm,
		})
	})

	protectedRoute := auth.Authenticate(tokens)(auth.RequireAdminWorkspace(auth.RequireAnyPermission(auth.PermissionContentEditor)(testHandler)))

	// 1. Request with Bearer iac_mcp_...
	req := httptest.NewRequest(http.MethodGet, "/admin/reading/materials", nil)
	req.Header.Set("Authorization", "Bearer "+createdKey.SecretKey)
	w := httptest.NewRecorder()
	protectedRoute.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res["role"] != auth.RoleAdmin || res["hasContent"] != true {
		t.Fatalf("unexpected auth response: %v", res)
	}

	// 2. Revoke key
	if err := mcpService.RevokeKey(context.Background(), createdKey.ID); err != nil {
		t.Fatalf("RevokeKey failed: %v", err)
	}

	// 3. Request with revoked key must fail with 401
	req = httptest.NewRequest(http.MethodGet, "/admin/reading/materials", nil)
	req.Header.Set("Authorization", "Bearer "+createdKey.SecretKey)
	w = httptest.NewRecorder()
	protectedRoute.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 Unauthorized for revoked key, got %d", w.Code)
	}
}
