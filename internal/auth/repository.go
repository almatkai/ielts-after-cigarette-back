package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	CreateUser(context.Context, string, string, string, string, []byte, time.Time) (UserView, error)
	FindUserByEmail(context.Context, string) (User, error)
	FindUserByID(context.Context, uuid.UUID) (UserView, error)
	CreateSession(context.Context, Session) error
	RotateSession(context.Context, []byte, Session, time.Time) (uuid.UUID, error)
	RevokeSession(context.Context, []byte, time.Time) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateUser(
	ctx context.Context,
	email, passwordHash, displayName, phone string,
	verificationTokenHash []byte,
	acceptedAt time.Time,
) (UserView, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return UserView{}, fmt.Errorf("begin registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var verificationID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM phone_verifications
		WHERE phone = $1
			AND purpose = 'REGISTRATION'
			AND verification_token_hash = $2
			AND verified_at IS NOT NULL
			AND consumed_at IS NULL
			AND token_expires_at > $3
		FOR UPDATE
	`, phone, verificationTokenHash, acceptedAt).Scan(&verificationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserView{}, ErrPhoneNotVerified
	}
	if err != nil {
		return UserView{}, fmt.Errorf("lock registration phone verification: %w", err)
	}

	userID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, phone, password_hash, role, terms_accepted_at)
		VALUES ($1, $2, $3, $4, 'STUDENT', $5)
	`, userID, email, phone, passwordHash, acceptedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_phone_unique" {
				return UserView{}, ErrPhoneExists
			}
			return UserView{}, ErrEmailExists
		}
		return UserView{}, fmt.Errorf("insert user: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (user_id, display_name)
		VALUES ($1, $2)
	`, userID, displayName); err != nil {
		return UserView{}, fmt.Errorf("insert user profile: %w", err)
	}

	for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_skill_progress (id, user_id, skill)
			VALUES ($1, $2, $3)
		`, uuid.New(), userID, skill); err != nil {
			return UserView{}, fmt.Errorf("insert %s skill progress: %w", skill, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE phone_verifications SET consumed_at = $2 WHERE id = $1
	`, verificationID, acceptedAt); err != nil {
		return UserView{}, fmt.Errorf("consume registration phone verification: %w", err)
	}

	view, err := scanUserView(tx.QueryRow(ctx, userViewQuery, userID))
	if err != nil {
		return UserView{}, fmt.Errorf("read registered user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UserView{}, fmt.Errorf("commit registration transaction: %w", err)
	}
	return view, nil
}

func (r *PostgresRepository) FindUserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := r.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role
		FROM users
		WHERE email = $1
	`, email).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user by email: %w", err)
	}
	return user, nil
}

func (r *PostgresRepository) FindUserByID(ctx context.Context, userID uuid.UUID) (UserView, error) {
	view, err := scanUserView(r.pool.QueryRow(ctx, userViewQuery, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return UserView{}, ErrUserNotFound
	}
	if err != nil {
		return UserView{}, fmt.Errorf("find user by ID: %w", err)
	}
	return view, nil
}

func (r *PostgresRepository) CreateSession(ctx context.Context, session Session) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_sessions (
			id, user_id, token_hash, expires_at, user_agent, ip_address
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''))
	`, session.ID, session.UserID, session.TokenHash, session.ExpiresAt, session.UserAgent, session.IPAddress)
	if err != nil {
		return fmt.Errorf("create refresh session: %w", err)
	}
	return nil
}

func (r *PostgresRepository) RotateSession(
	ctx context.Context,
	oldHash []byte,
	replacement Session,
	now time.Time,
) (uuid.UUID, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin refresh rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sessionID, userID uuid.UUID
	var expiresAt time.Time
	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, expires_at, revoked_at
		FROM refresh_sessions
		WHERE token_hash = $1
		FOR UPDATE
	`, oldHash).Scan(&sessionID, &userID, &expiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidRefresh
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("lock refresh session: %w", err)
	}

	if revokedAt != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE refresh_sessions
			SET revoked_at = COALESCE(revoked_at, $2)
			WHERE user_id = $1 AND revoked_at IS NULL
		`, userID, now); err != nil {
			return uuid.Nil, fmt.Errorf("revoke sessions after token reuse: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, fmt.Errorf("commit token reuse revocation: %w", err)
		}
		return uuid.Nil, ErrRefreshReuse
	}
	if !expiresAt.After(now) {
		if _, err := tx.Exec(ctx, `
			UPDATE refresh_sessions SET revoked_at = $2 WHERE id = $1
		`, sessionID, now); err != nil {
			return uuid.Nil, fmt.Errorf("revoke expired refresh session: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, fmt.Errorf("commit expired refresh revocation: %w", err)
		}
		return uuid.Nil, ErrInvalidRefresh
	}

	replacement.UserID = userID
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_sessions (
			id, user_id, token_hash, expires_at, user_agent, ip_address
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''))
	`, replacement.ID, userID, replacement.TokenHash, replacement.ExpiresAt,
		replacement.UserAgent, replacement.IPAddress); err != nil {
		return uuid.Nil, fmt.Errorf("insert replacement refresh session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE refresh_sessions
		SET revoked_at = $2, replaced_by = $3
		WHERE id = $1
	`, sessionID, now, replacement.ID); err != nil {
		return uuid.Nil, fmt.Errorf("revoke rotated refresh session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit refresh rotation: %w", err)
	}
	return userID, nil
}

func (r *PostgresRepository) RevokeSession(ctx context.Context, hash []byte, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE refresh_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE token_hash = $1
	`, hash, now)
	if err != nil {
		return fmt.Errorf("revoke refresh session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidRefresh
	}
	return nil
}

const userViewQuery = `
	SELECT
		u.id,
		u.email,
		COALESCE(u.phone, ''),
		p.display_name,
		u.role,
		p.current_band::double precision,
		p.target_band::double precision,
		p.exam_date,
		p.exam_type,
		p.timezone,
		u.created_at,
		GREATEST(u.updated_at, p.updated_at)
	FROM users u
	JOIN user_profiles p ON p.user_id = u.id
	WHERE u.id = $1
`

type rowScanner interface {
	Scan(...any) error
}

func scanUserView(row rowScanner) (UserView, error) {
	var view UserView
	var examDate *time.Time
	if err := row.Scan(
		&view.ID,
		&view.Email,
		&view.Phone,
		&view.DisplayName,
		&view.Role,
		&view.CurrentBand,
		&view.TargetBand,
		&examDate,
		&view.ExamType,
		&view.Timezone,
		&view.CreatedAt,
		&view.UpdatedAt,
	); err != nil {
		return UserView{}, err
	}
	if examDate != nil {
		value := examDate.Format(time.DateOnly)
		view.ExamDate = &value
	}
	view.CreatedAt = view.CreatedAt.UTC()
	view.UpdatedAt = view.UpdatedAt.UTC()
	return view, nil
}
