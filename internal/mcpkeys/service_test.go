package mcpkeys

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/google/uuid"
)

type mockRepo struct {
	mu     sync.Mutex
	keys   map[uuid.UUID]MCPKey
	hashes map[string]uuid.UUID
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		keys:   make(map[uuid.UUID]MCPKey),
		hashes: make(map[string]uuid.UUID),
	}
}

func (m *mockRepo) Create(ctx context.Context, key MCPKey, keyHash string) (*MCPKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[key.ID] = key
	m.hashes[keyHash] = key.ID
	k := key
	return &k, nil
}

func (m *mockRepo) List(ctx context.Context) ([]MCPKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []MCPKey
	for _, k := range m.keys {
		list = append(list, k)
	}
	return list, nil
}

func (m *mockRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok || k.RevokedAt != nil {
		return ErrKeyNotFound
	}
	now := time.Now().UTC()
	k.RevokedAt = &now
	m.keys[id] = k
	return nil
}

func (m *mockRepo) FindByHash(ctx context.Context, keyHash string) (*MCPKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.hashes[keyHash]
	if !ok {
		return nil, ErrKeyNotFound
	}
	k := m.keys[id]
	if k.RevokedAt != nil {
		return nil, ErrKeyRevoked
	}
	return &k, nil
}

func (m *mockRepo) TouchLastUsed(ctx context.Context, id uuid.UUID, lastUsed time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return ErrKeyNotFound
	}
	k.LastUsedAt = &lastUsed
	m.keys[id] = k
	return nil
}

type mockUserFinder struct {
	users map[uuid.UUID]auth.UserView
}

func (f *mockUserFinder) FindUserByID(ctx context.Context, userID uuid.UUID) (auth.UserView, error) {
	u, ok := f.users[userID]
	if !ok {
		return auth.UserView{}, auth.ErrUserNotFound
	}
	return u, nil
}

func TestService_GenerateAndValidateKey(t *testing.T) {
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
	ctx := context.Background()

	created, err := service.GenerateKey(ctx, "Test Local CLI", adminID)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	if !strings.HasPrefix(created.SecretKey, "iac_mcp_") {
		t.Errorf("SecretKey does not have prefix 'iac_mcp_': %s", created.SecretKey)
	}
	if created.Name != "Test Local CLI" {
		t.Errorf("expected name 'Test Local CLI', got %s", created.Name)
	}

	// Validate valid key
	userID, role, permissions, err := service.ValidateAPIKey(ctx, created.SecretKey)
	if err != nil {
		t.Fatalf("ValidateAPIKey failed: %v", err)
	}
	if userID != adminID {
		t.Errorf("expected userID %s, got %s", adminID, userID)
	}
	if role != auth.RoleAdmin {
		t.Errorf("expected role %s, got %s", auth.RoleAdmin, role)
	}
	if len(permissions) == 0 {
		t.Errorf("expected non-empty permissions")
	}

	// Revoke key
	if err := service.RevokeKey(ctx, created.ID); err != nil {
		t.Fatalf("RevokeKey failed: %v", err)
	}

	// Validate after revocation must fail
	_, _, _, err = service.ValidateAPIKey(ctx, created.SecretKey)
	if err == nil {
		t.Fatalf("expected error validating revoked key, got nil")
	}
}

func TestService_ValidateKey_InvalidFormat(t *testing.T) {
	service := NewService(newMockRepo(), &mockUserFinder{})
	_, _, _, err := service.ValidateAPIKey(context.Background(), "invalid-token-without-prefix")
	if err == nil {
		t.Fatalf("expected error for invalid secret format, got nil")
	}
}

func TestService_ValidateKey_NonAdminRevoked(t *testing.T) {
	repo := newMockRepo()
	studentID := uuid.New()
	userFinder := &mockUserFinder{
		users: map[uuid.UUID]auth.UserView{
			studentID: {
				ID:   studentID,
				Role: auth.RoleStudent, // Demoted to student
			},
		},
	}

	service := NewService(repo, userFinder)
	ctx := context.Background()

	created, err := service.GenerateKey(ctx, "Student Key", studentID)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	_, _, _, err = service.ValidateAPIKey(ctx, created.SecretKey)
	if err == nil {
		t.Fatalf("expected error validating key of non-admin, got nil")
	}
}
