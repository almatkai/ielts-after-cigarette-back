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

// Create inserts a waitlist lead as a users row with status WAITING: no
// password and no terms acceptance yet — registration completes the same
// row. users.email is NOT NULL, so a lead without an email gets a unique
// placeholder address that reads back as NULL.
func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Entry, error) {
	entryID := uuid.New()
	email := params.Email
	if email == "" {
		email = placeholderEmail(entryID)
	}
	var entry Entry
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (
			id, email, phone, role, status, first_name, last_name, source,
			google_sub, referral_code, referred_by_code, created_at
		) VALUES ($1, $2, $3, 'STUDENT', 'WAITING', NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), $8, NULLIF($9, ''), $10)
		RETURNING id, phone, `+visibleEmailColumn+`, first_name, last_name, source,
			google_sub, referral_code, referred_by_code, status, created_at
	`, entryID, email, params.Phone, params.FirstName, params.LastName, params.Source,
		params.GoogleSub, params.ReferralCode, params.ReferredByCode, params.CreatedAt).Scan(
		&entry.ID,
		&entry.Phone,
		&entry.Email,
		&entry.FirstName,
		&entry.LastName,
		&entry.Source,
		&entry.GoogleSub,
		&entry.ReferralCode,
		&entry.ReferredByCode,
		&entry.Status,
		&entry.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_referral_code_unique" {
				return Entry{}, ErrReferralCodeCollision
			}
			return Entry{}, ErrEntryExists
		}
		return Entry{}, fmt.Errorf("insert waitlist entry: %w", err)
	}
	entry.CreatedAt = entry.CreatedAt.UTC()
	return entry, nil
}

func (r *PostgresRepository) List(ctx context.Context) ([]Entry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, COALESCE(u.phone, ''), `+visibleEmailColumnAliased+`,
			u.first_name, u.last_name, u.source, u.google_sub,
			COALESCE(u.referral_code, ''), u.referred_by_code,
			(SELECT COUNT(*) FROM users c WHERE c.referred_by_code = u.referral_code) AS referrals,
			u.status, NULL::timestamptz AS phone_verified_at, u.created_at
		FROM users u
		WHERE u.status IN ('WAITING', 'INVITED')
		ORDER BY u.created_at DESC
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
			&entry.ReferralCode,
			&entry.ReferredByCode,
			&entry.Referrals,
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
		SELECT EXISTS (SELECT 1 FROM users WHERE google_sub = $1)
	`, googleSub).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check waitlist google sub: %w", err)
	}
	return exists, nil
}

func (r *PostgresRepository) ExistsByPhone(ctx context.Context, phone string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE phone = $1)
	`, phone).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check waitlist phone: %w", err)
	}
	return exists, nil
}

// users.email is NOT NULL, so a lead without an email is stored under a
// unique placeholder address. The placeholders are internal: every read path
// maps them back to NULL.
const (
	placeholderEmailDomain  = "@placeholder.invalid"
	placeholderEmailPattern = "waitlist-%" + placeholderEmailDomain
	visibleEmailColumn      = `CASE WHEN email LIKE '` + placeholderEmailPattern + `' THEN NULL ELSE email END`
	visibleEmailColumnAliased = `CASE WHEN u.email LIKE '` + placeholderEmailPattern + `' THEN NULL ELSE u.email END`
)

func placeholderEmail(id uuid.UUID) string {
	return "waitlist-" + id.String() + placeholderEmailDomain
}
