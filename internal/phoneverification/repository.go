package phoneverification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateChallenge(
	ctx context.Context,
	challenge Challenge,
	resendInterval time.Duration,
) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin phone verification transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	lockKey := challenge.Phone + ":" + string(challenge.Purpose)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock phone verification: %w", err)
	}

	var lastCreatedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT created_at
		FROM phone_verifications
		WHERE phone = $1 AND purpose = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, challenge.Phone, challenge.Purpose).Scan(&lastCreatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read latest phone verification: %w", err)
	}
	if err == nil && lastCreatedAt.After(challenge.CreatedAt.Add(-resendInterval)) {
		return ErrResendTooSoon
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO phone_verifications (
			id, phone, purpose, code_hash, max_attempts, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, challenge.ID, challenge.Phone, challenge.Purpose, challenge.CodeHash,
		challenge.MaxAttempts, challenge.ExpiresAt, challenge.CreatedAt); err != nil {
		return fmt.Errorf("insert phone verification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit phone verification: %w", err)
	}
	return nil
}

func (r *PostgresRepository) VerifyChallenge(
	ctx context.Context,
	id uuid.UUID,
	phone string,
	purpose Purpose,
	codeHash []byte,
	tokenHash []byte,
	now time.Time,
	tokenExpiresAt time.Time,
) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE phone_verifications
		SET attempts = attempts + 1,
			verified_at = $5,
			verification_token_hash = $6,
			token_expires_at = $7
		WHERE id = $1
			AND phone = $2
			AND purpose = $3
			AND code_hash = $4
			AND expires_at > $5
			AND verified_at IS NULL
			AND consumed_at IS NULL
			AND attempts < max_attempts
	`, id, phone, purpose, codeHash, now, tokenHash, tokenExpiresAt)
	if err != nil {
		return fmt.Errorf("verify phone challenge: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	if _, err := r.pool.Exec(ctx, `
		UPDATE phone_verifications
		SET attempts = attempts + 1
		WHERE id = $1
			AND phone = $2
			AND purpose = $3
			AND expires_at > $4
			AND verified_at IS NULL
			AND consumed_at IS NULL
			AND attempts < max_attempts
	`, id, phone, purpose, now); err != nil {
		return fmt.Errorf("record invalid phone verification attempt: %w", err)
	}
	return ErrInvalidCode
}
