package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidRole  = errors.New("invalid role")
	ErrUserNotFound = errors.New("user not found")
)

type RoleChange struct {
	UserID uuid.UUID `json:"userId"`
	Email  string    `json:"email"`
	Role   string    `json:"role"`
}

type RoleRepository interface {
	SetRole(context.Context, string, string, time.Time) (RoleChange, error)
}

type RoleService struct {
	repository RoleRepository
	now        func() time.Time
}

func NewRoleService(repository RoleRepository) *RoleService {
	return &RoleService{repository: repository, now: time.Now}
}

func (s *RoleService) SetRole(ctx context.Context, email, role string) (RoleChange, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	role = auth.NormalizeRole(role)
	if email == "" || !strings.Contains(email, "@") {
		return RoleChange{}, fmt.Errorf("email is required")
	}
	if !auth.ValidRole(role) {
		return RoleChange{}, ErrInvalidRole
	}
	return s.repository.SetRole(ctx, email, role, s.now().UTC())
}

type PostgresRoleRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRoleRepository(pool *pgxpool.Pool) *PostgresRoleRepository {
	return &PostgresRoleRepository{pool: pool}
}

func (r *PostgresRoleRepository) SetRole(ctx context.Context, email, role string, changedAt time.Time) (RoleChange, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RoleChange{}, fmt.Errorf("begin role change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var result RoleChange
	err = tx.QueryRow(ctx, `
		UPDATE users
		SET role = $1
		WHERE email = $2
		RETURNING id, email, role
	`, role, email).Scan(&result.UserID, &result.Email, &result.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleChange{}, ErrUserNotFound
	}
	if err != nil {
		return RoleChange{}, fmt.Errorf("update user role: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE refresh_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, result.UserID, changedAt); err != nil {
		return RoleChange{}, fmt.Errorf("revoke sessions after role change: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RoleChange{}, fmt.Errorf("commit role change: %w", err)
	}
	return result, nil
}
