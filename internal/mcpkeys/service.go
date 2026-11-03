package mcpkeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/google/uuid"
)

var (
	ErrInvalidKeyName = errors.New("key name is required")
	ErrInvalidSecret  = errors.New("invalid mcp key format")
	ErrUserForbidden  = errors.New("creator user is not an active admin")
)

type UserFinder interface {
	FindUserByID(ctx context.Context, userID uuid.UUID) (auth.UserView, error)
}

type Service struct {
	repo       Repository
	userFinder UserFinder
	now        func() time.Time
}

func NewService(repo Repository, userFinder UserFinder) *Service {
	return &Service{
		repo:       repo,
		userFinder: userFinder,
		now:        time.Now,
	}
}

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) GenerateKey(ctx context.Context, name string, createdBy uuid.UUID) (*CreateKeyResponse, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Antigravity CLI"
	}

	randomBytes := make([]byte, 24)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}

	secretKey := "iac_mcp_" + hex.EncodeToString(randomBytes)
	keyHash := hashKey(secretKey)
	keyPrefix := secretKey[:16] + "..."

	now := s.now().UTC()
	key := MCPKey{
		ID:        uuid.New(),
		Name:      name,
		KeyPrefix: keyPrefix,
		CreatedBy: createdBy,
		CreatedAt: now,
	}

	created, err := s.repo.Create(ctx, key, keyHash)
	if err != nil {
		return nil, fmt.Errorf("store mcp key: %w", err)
	}

	return &CreateKeyResponse{
		MCPKey:    *created,
		SecretKey: secretKey,
	}, nil
}

func (s *Service) ListKeys(ctx context.Context) ([]MCPKey, error) {
	return s.repo.List(ctx)
}

func (s *Service) RevokeKey(ctx context.Context, id uuid.UUID) error {
	return s.repo.Revoke(ctx, id)
}

func (s *Service) ValidateAPIKey(ctx context.Context, secretKey string) (uuid.UUID, string, []string, error) {
	secretKey = strings.TrimSpace(secretKey)
	if !strings.HasPrefix(secretKey, "iac_mcp_") {
		return uuid.Nil, "", nil, ErrInvalidSecret
	}

	keyHash := hashKey(secretKey)
	key, err := s.repo.FindByHash(ctx, keyHash)
	if err != nil {
		return uuid.Nil, "", nil, err
	}

	// Verify creator account still has ADMIN privileges
	if s.userFinder != nil {
		user, err := s.userFinder.FindUserByID(ctx, key.CreatedBy)
		if err != nil {
			return uuid.Nil, "", nil, fmt.Errorf("lookup mcp key creator: %w", err)
		}
		if auth.NormalizeRole(user.Role) != auth.RoleAdmin {
			return uuid.Nil, "", nil, ErrUserForbidden
		}
	}

	// Update last_used_at asynchronously or non-blockingly
	go func(id uuid.UUID, touched time.Time) {
		_ = s.repo.TouchLastUsed(context.Background(), id, touched)
	}(key.ID, s.now().UTC())

	permissions := []string{
		auth.PermissionContentEditor,
		auth.PermissionBlogModerator,
	}

	return key.CreatedBy, auth.RoleAdmin, permissions, nil
}
