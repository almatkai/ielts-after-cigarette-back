package waitlist

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	Create(context.Context, CreateParams) (Entry, error)
	ExistsByGoogleSub(ctx context.Context, googleSub string) (bool, error)
	ExistsByPhone(ctx context.Context, phone string) (bool, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Entry, error) {
	entryID := uuid.New()
	var entry Entry
	err := r.pool.QueryRow(ctx, `
		INSERT INTO waitlist_entries (
			id, phone, email, first_name, last_name, source, google_sub, created_at
		) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), $8)
		RETURNING id, phone, email, first_name, last_name, source, google_sub, status, phone_verified_at, created_at
	`, entryID, params.Phone, params.Email, params.FirstName, params.LastName, params.Source,
		params.GoogleSub, params.CreatedAt).Scan(
		&entry.ID,
		&entry.Phone,
		&entry.Email,
		&entry.FirstName,
		&entry.LastName,
		&entry.Source,
		&entry.GoogleSub,
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
	entry.CreatedAt = entry.CreatedAt.UTC()
	return entry, nil
}

func (r *PostgresRepository) ExistsByGoogleSub(ctx context.Context, googleSub string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM waitlist_entries WHERE google_sub = $1)
	`, googleSub).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check waitlist google sub: %w", err)
	}
	return exists, nil
}

func (r *PostgresRepository) ExistsByPhone(ctx context.Context, phone string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM waitlist_entries WHERE phone = $1)
	`, phone).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check waitlist phone: %w", err)
	}
	return exists, nil
}
