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
	List(ctx context.Context) ([]Entry, error)
	IsAdmin(ctx context.Context, email string) (bool, error)
	ListAdmins(ctx context.Context) ([]string, error)
	AddAdmin(ctx context.Context, email string) error
	RemoveAdmin(ctx context.Context, email string) error
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

func (r *PostgresRepository) List(ctx context.Context) ([]Entry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, phone, email, first_name, last_name, source, google_sub, status, phone_verified_at, created_at
		FROM waitlist_entries
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list waitlist entries: %w", err)
	}
	defer rows.Close()

	entries := make([]Entry, 0)
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(
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
		); err != nil {
			return nil, fmt.Errorf("scan waitlist entry: %w", err)
		}
		entry.CreatedAt = entry.CreatedAt.UTC()
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate waitlist entries: %w", err)
	}
	return entries, nil
}

func (r *PostgresRepository) IsAdmin(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM super_admins WHERE email = $1)
	`, email).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check super admin: %w", err)
	}
	return exists, nil
}

func (r *PostgresRepository) ListAdmins(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT email FROM super_admins ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list super admins: %w", err)
	}
	defer rows.Close()

	emails := make([]string, 0)
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("scan super admin: %w", err)
		}
		emails = append(emails, email)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate super admins: %w", err)
	}
	return emails, nil
}

func (r *PostgresRepository) AddAdmin(ctx context.Context, email string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO super_admins (email) VALUES ($1)
		ON CONFLICT (email) DO NOTHING
	`, email)
	if err != nil {
		return fmt.Errorf("add super admin: %w", err)
	}
	return nil
}

func (r *PostgresRepository) RemoveAdmin(ctx context.Context, email string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM super_admins WHERE email = $1
	`, email)
	if err != nil {
		return fmt.Errorf("remove super admin: %w", err)
	}
	return nil
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
