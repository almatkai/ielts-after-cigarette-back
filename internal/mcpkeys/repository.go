package mcpkeys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrKeyNotFound = errors.New("mcp key not found")
	ErrKeyRevoked  = errors.New("mcp key revoked")
)

type Repository interface {
	Create(ctx context.Context, key MCPKey, keyHash string) (*MCPKey, error)
	List(ctx context.Context) ([]MCPKey, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	FindByHash(ctx context.Context, keyHash string) (*MCPKey, error)
	TouchLastUsed(ctx context.Context, id uuid.UUID, lastUsed time.Time) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, key MCPKey, keyHash string) (*MCPKey, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres pool is not configured")
	}

	var created MCPKey
	err := r.pool.QueryRow(ctx, `
		INSERT INTO mcp_api_keys (id, name, key_prefix, key_hash, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, key_prefix, created_by, created_at, last_used_at, revoked_at
	`, key.ID, key.Name, key.KeyPrefix, keyHash, key.CreatedBy, key.CreatedAt).Scan(
		&created.ID,
		&created.Name,
		&created.KeyPrefix,
		&created.CreatedBy,
		&created.CreatedAt,
		&created.LastUsedAt,
		&created.RevokedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create mcp key: %w", err)
	}

	return &created, nil
}

func (r *PostgresRepository) List(ctx context.Context) ([]MCPKey, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres pool is not configured")
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, name, key_prefix, created_by, created_at, last_used_at, revoked_at
		FROM mcp_api_keys
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list mcp keys: %w", err)
	}
	defer rows.Close()

	var keys []MCPKey
	for rows.Next() {
		var k MCPKey
		if err := rows.Scan(
			&k.ID,
			&k.Name,
			&k.KeyPrefix,
			&k.CreatedBy,
			&k.CreatedAt,
			&k.LastUsedAt,
			&k.RevokedAt,
		); err != nil {
			return nil, fmt.Errorf("scan mcp key: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp keys: %w", err)
	}

	if keys == nil {
		keys = []MCPKey{}
	}
	return keys, nil
}

func (r *PostgresRepository) Revoke(ctx context.Context, id uuid.UUID) error {
	if r.pool == nil {
		return fmt.Errorf("postgres pool is not configured")
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE mcp_api_keys
		SET revoked_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("revoke mcp key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrKeyNotFound
	}
	return nil
}

func (r *PostgresRepository) FindByHash(ctx context.Context, keyHash string) (*MCPKey, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres pool is not configured")
	}

	var k MCPKey
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, key_prefix, created_by, created_at, last_used_at, revoked_at
		FROM mcp_api_keys
		WHERE key_hash = $1
	`, keyHash).Scan(
		&k.ID,
		&k.Name,
		&k.KeyPrefix,
		&k.CreatedBy,
		&k.CreatedAt,
		&k.LastUsedAt,
		&k.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find mcp key by hash: %w", err)
	}

	if k.RevokedAt != nil {
		return nil, ErrKeyRevoked
	}

	return &k, nil
}

func (r *PostgresRepository) TouchLastUsed(ctx context.Context, id uuid.UUID, lastUsed time.Time) error {
	if r.pool == nil {
		return fmt.Errorf("postgres pool is not configured")
	}

	_, err := r.pool.Exec(ctx, `
		UPDATE mcp_api_keys
		SET last_used_at = $2
		WHERE id = $1
	`, id, lastUsed)
	if err != nil {
		return fmt.Errorf("touch mcp key last used: %w", err)
	}
	return nil
}
