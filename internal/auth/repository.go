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
	CreateGoogleUser(ctx context.Context, email, passwordHash, displayName, role, googleSub string, now time.Time) (UserView, error)
	CreateGoogleCompletedUser(ctx context.Context, email, passwordHash, displayName, phone, googleSub string, now time.Time) (UserView, error)
	CompleteWaitlistUser(ctx context.Context, userID uuid.UUID, email, passwordHash, displayName, phone, googleSub string, verificationTokenHash []byte, now time.Time) (UserView, error)
	UpgradeWaitlistToAdmin(ctx context.Context, userID uuid.UUID, displayName, googleSub string, now time.Time) (UserView, error)
	SetRole(ctx context.Context, email, role string) error
	LinkGoogleSub(ctx context.Context, userID uuid.UUID, googleSub string) error
	FindUserByEmail(context.Context, string) (User, error)
	FindUserByGoogleSub(context.Context, string) (User, error)
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

func (r *PostgresRepository) CreateGoogleUser(
	ctx context.Context,
	email, passwordHash, displayName, role, googleSub string,
	now time.Time,
) (UserView, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return UserView{}, fmt.Errorf("begin google user transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	userID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, terms_accepted_at, google_sub)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
	`, userID, email, passwordHash, role, now, googleSub); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return UserView{}, ErrEmailExists
		}
		return UserView{}, fmt.Errorf("insert google user: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (user_id, display_name)
		VALUES ($1, $2)
	`, userID, displayName); err != nil {
		return UserView{}, fmt.Errorf("insert google user profile: %w", err)
	}

	for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_skill_progress (id, user_id, skill)
			VALUES ($1, $2, $3)
		`, uuid.New(), userID, skill); err != nil {
			return UserView{}, fmt.Errorf("insert %s skill progress: %w", skill, err)
		}
	}

	view, err := scanUserView(tx.QueryRow(ctx, userViewQuery, userID))
	if err != nil {
		return UserView{}, fmt.Errorf("read google user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UserView{}, fmt.Errorf("commit google user transaction: %w", err)
	}
	return view, nil
}

// CreateGoogleCompletedUser registers a Google-sign-in student after the
// complete-registration step: email comes from the verified Google token, the
// password is user-chosen, and the phone needs no WhatsApp proof — Google
// identity is the verification. Same row set as CreateUser, minus the phone
// verification token consumption.
func (r *PostgresRepository) CreateGoogleCompletedUser(
	ctx context.Context,
	email, passwordHash, displayName, phone, googleSub string,
	now time.Time,
) (UserView, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return UserView{}, fmt.Errorf("begin google registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	userID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, phone, password_hash, role, terms_accepted_at, google_sub)
		VALUES ($1, $2, $3, $4, 'STUDENT', $5, NULLIF($6, ''))
	`, userID, email, phone, passwordHash, now, googleSub); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_phone_unique" {
				return UserView{}, ErrPhoneExists
			}
			return UserView{}, ErrEmailExists
		}
		return UserView{}, fmt.Errorf("insert google registered user: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (user_id, display_name)
		VALUES ($1, $2)
	`, userID, displayName); err != nil {
		return UserView{}, fmt.Errorf("insert google registered user profile: %w", err)
	}

	for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_skill_progress (id, user_id, skill)
			VALUES ($1, $2, $3)
		`, uuid.New(), userID, skill); err != nil {
			return UserView{}, fmt.Errorf("insert %s skill progress: %w", skill, err)
		}
	}

	view, err := scanUserView(tx.QueryRow(ctx, userViewQuery, userID))
	if err != nil {
		return UserView{}, fmt.Errorf("read google registered user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UserView{}, fmt.Errorf("commit google registration transaction: %w", err)
	}
	return view, nil
}

// CompleteWaitlistUser turns a waitlist lead row (status WAITING/INVITED) into
// a registered student in place, reusing the row created from the waitlist
// entry. When verificationTokenHash is non-nil, a verified, unconsumed
// REGISTRATION phone verification is required and consumed (same rule as
// CreateUser); Google-completed registrations pass nil because the Google
// identity is the verification.
func (r *PostgresRepository) CompleteWaitlistUser(
	ctx context.Context,
	userID uuid.UUID,
	email, passwordHash, displayName, phone, googleSub string,
	verificationTokenHash []byte,
	now time.Time,
) (UserView, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return UserView{}, fmt.Errorf("begin waitlist completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var verificationID uuid.UUID
	if verificationTokenHash != nil {
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
		`, phone, verificationTokenHash, now).Scan(&verificationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return UserView{}, ErrPhoneNotVerified
		}
		if err != nil {
			return UserView{}, fmt.Errorf("lock registration phone verification: %w", err)
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET email = COALESCE(NULLIF($2, ''), email),
			password_hash = $3,
			terms_accepted_at = $4,
			status = 'REGISTERED',
			phone = COALESCE(NULLIF($5, ''), phone),
			google_sub = COALESCE(google_sub, NULLIF($6, '')),
			updated_at = $4
		WHERE id = $1 AND status IN ('WAITING', 'INVITED')
	`, userID, email, passwordHash, now, phone, googleSub)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_phone_unique" {
				return UserView{}, ErrPhoneExists
			}
			return UserView{}, ErrEmailExists
		}
		return UserView{}, fmt.Errorf("complete waitlist user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return UserView{}, ErrUserNotFound
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (user_id, display_name)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, displayName); err != nil {
		return UserView{}, fmt.Errorf("insert waitlist user profile: %w", err)
	}

	for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_skill_progress (id, user_id, skill)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, skill) DO NOTHING
		`, uuid.New(), userID, skill); err != nil {
			return UserView{}, fmt.Errorf("insert %s skill progress: %w", skill, err)
		}
	}

	if verificationTokenHash != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE phone_verifications SET consumed_at = $2 WHERE id = $1
		`, verificationID, now); err != nil {
			return UserView{}, fmt.Errorf("consume registration phone verification: %w", err)
		}
	}

	view, err := scanUserView(tx.QueryRow(ctx, userViewQuery, userID))
	if err != nil {
		return UserView{}, fmt.Errorf("read completed waitlist user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UserView{}, fmt.Errorf("commit waitlist completion transaction: %w", err)
	}
	return view, nil
}

// UpgradeWaitlistToAdmin promotes a waitlist lead row to a registered admin
// signing in via Google (super-admin allowlist). No password is set and no
// phone verification is required.
func (r *PostgresRepository) UpgradeWaitlistToAdmin(
	ctx context.Context,
	userID uuid.UUID,
	displayName, googleSub string,
	now time.Time,
) (UserView, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return UserView{}, fmt.Errorf("begin waitlist admin upgrade transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET role = 'ADMIN',
			status = 'REGISTERED',
			terms_accepted_at = $2,
			google_sub = COALESCE(google_sub, NULLIF($3, '')),
			updated_at = $2
		WHERE id = $1 AND status IN ('WAITING', 'INVITED')
	`, userID, now, googleSub)
	if err != nil {
		return UserView{}, fmt.Errorf("upgrade waitlist user to admin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return UserView{}, ErrUserNotFound
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (user_id, display_name)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, displayName); err != nil {
		return UserView{}, fmt.Errorf("insert waitlist admin profile: %w", err)
	}

	for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_skill_progress (id, user_id, skill)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, skill) DO NOTHING
		`, uuid.New(), userID, skill); err != nil {
			return UserView{}, fmt.Errorf("insert %s skill progress: %w", skill, err)
		}
	}

	view, err := scanUserView(tx.QueryRow(ctx, userViewQuery, userID))
	if err != nil {
		return UserView{}, fmt.Errorf("read upgraded waitlist admin: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UserView{}, fmt.Errorf("commit waitlist admin upgrade transaction: %w", err)
	}
	return view, nil
}

func (r *PostgresRepository) SetRole(ctx context.Context, email, role string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET role = $2 WHERE email = $1
	`, email, role)
	if err != nil {
		return fmt.Errorf("set user role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *PostgresRepository) FindUserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := r.pool.QueryRow(ctx, `
		SELECT id, email, COALESCE(password_hash, ''), role, COALESCE(google_sub, ''),
			status, COALESCE(phone, ''), COALESCE(first_name, ''), COALESCE(last_name, '')
		FROM users
		WHERE email = $1
	`, email).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.GoogleSub,
		&user.Status, &user.Phone, &user.FirstName, &user.LastName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user by email: %w", err)
	}
	return user, nil
}

func (r *PostgresRepository) FindUserByGoogleSub(ctx context.Context, googleSub string) (User, error) {
	var user User
	err := r.pool.QueryRow(ctx, `
		SELECT id, email, COALESCE(password_hash, ''), role, COALESCE(google_sub, ''),
			status, COALESCE(phone, ''), COALESCE(first_name, ''), COALESCE(last_name, '')
		FROM users
		WHERE google_sub = $1
	`, googleSub).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.GoogleSub,
		&user.Status, &user.Phone, &user.FirstName, &user.LastName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user by google sub: %w", err)
	}
	return user, nil
}

// LinkGoogleSub attaches a Google identity to an existing account that signed
// up with the same email. Only fills an empty google_sub; it never overwrites.
// A unique-violation race (the sub was linked to another account first) must
// not break sign-in, so it is ignored.
func (r *PostgresRepository) LinkGoogleSub(ctx context.Context, userID uuid.UUID, googleSub string) error {
	if _, err := r.pool.Exec(ctx, `
		UPDATE users SET google_sub = $2 WHERE id = $1 AND google_sub IS NULL
	`, userID, googleSub); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil
		}
		return fmt.Errorf("link google sub: %w", err)
	}
	return nil
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
		COALESCE(p.display_name, ''),
		u.role,
		p.current_band::double precision,
		p.target_band::double precision,
		p.exam_date,
		p.exam_type,
		COALESCE(p.timezone, 'UTC'),
		u.created_at,
		GREATEST(u.updated_at, p.updated_at)
	FROM users u
	LEFT JOIN user_profiles p ON p.user_id = u.id
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
