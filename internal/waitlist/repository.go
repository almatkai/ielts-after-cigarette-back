package waitlist

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/phoneverification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	Create(context.Context, CreateParams) (Entry, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Entry, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Entry{}, fmt.Errorf("begin waitlist transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var verificationID uuid.UUID
	var verifiedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, verified_at
		FROM phone_verifications
		WHERE phone = $1
			AND purpose = $2
			AND verification_token_hash = $3
			AND verified_at IS NOT NULL
			AND consumed_at IS NULL
			AND token_expires_at > $4
		FOR UPDATE
	`, params.Phone, phoneverification.PurposeWaitlist,
		params.VerificationTokenHash, params.CreatedAt).Scan(&verificationID, &verifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrPhoneNotVerified
	}
	if err != nil {
		return Entry{}, fmt.Errorf("lock waitlist phone verification: %w", err)
	}

	entryID := uuid.New()
	var entry Entry
	err = tx.QueryRow(ctx, `
		INSERT INTO waitlist_entries (
			id, phone, email, first_name, last_name, source, phone_verified_at, created_at
		) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7, $8)
		RETURNING id, phone, email, first_name, last_name, source, status, phone_verified_at, created_at
	`, entryID, params.Phone, params.Email, params.FirstName, params.LastName, params.Source,
		verifiedAt, params.CreatedAt).Scan(
		&entry.ID,
		&entry.Phone,
		&entry.Email,
		&entry.FirstName,
		&entry.LastName,
		&entry.Source,
		&entry.Status,
		&entry.PhoneVerifiedAt,
		&entry.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Entry{}, ErrEntryExists
		}
		return Entry{}, fmt.Errorf("insert waitlist entry: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE phone_verifications SET consumed_at = $2 WHERE id = $1
	`, verificationID, params.CreatedAt); err != nil {
		return Entry{}, fmt.Errorf("consume waitlist phone verification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Entry{}, fmt.Errorf("commit waitlist transaction: %w", err)
	}
	entry.PhoneVerifiedAt = entry.PhoneVerifiedAt.UTC()
	entry.CreatedAt = entry.CreatedAt.UTC()
	return entry, nil
}
