package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	Get(context.Context, uuid.UUID) (Profile, error)
	UpdateProfile(context.Context, uuid.UUID, ProfilePatch) (Profile, error)
	UpdateGoal(context.Context, uuid.UUID, float64, time.Time, string) (Profile, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Get(ctx context.Context, userID uuid.UUID) (Profile, error) {
	profile, err := scanProfile(r.pool.QueryRow(ctx, profileQuery, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get profile: %w", err)
	}
	return profile, nil
}

func (r *PostgresRepository) UpdateProfile(
	ctx context.Context,
	userID uuid.UUID,
	patch ProfilePatch,
) (Profile, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE user_profiles
		SET
			display_name = COALESCE($2, display_name),
			timezone = COALESCE($3, timezone)
		WHERE user_id = $1
	`, userID, patch.DisplayName, patch.Timezone)
	if err != nil {
		return Profile{}, fmt.Errorf("update profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Profile{}, ErrNotFound
	}
	return r.Get(ctx, userID)
}

func (r *PostgresRepository) UpdateGoal(
	ctx context.Context,
	userID uuid.UUID,
	targetBand float64,
	examDate time.Time,
	examType string,
) (Profile, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE user_profiles
		SET target_band = $2, exam_date = $3, exam_type = $4
		WHERE user_id = $1
	`, userID, targetBand, examDate, examType)
	if err != nil {
		return Profile{}, fmt.Errorf("update profile goal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Profile{}, ErrNotFound
	}
	return r.Get(ctx, userID)
}

const profileQuery = `
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

func scanProfile(row rowScanner) (Profile, error) {
	var profile Profile
	var examDate *time.Time
	if err := row.Scan(
		&profile.ID,
		&profile.Email,
		&profile.Phone,
		&profile.DisplayName,
		&profile.Role,
		&profile.CurrentBand,
		&profile.TargetBand,
		&examDate,
		&profile.ExamType,
		&profile.Timezone,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	); err != nil {
		return Profile{}, err
	}
	if examDate != nil {
		value := examDate.Format(time.DateOnly)
		profile.ExamDate = &value
	}
	profile.CreatedAt = profile.CreatedAt.UTC()
	profile.UpdatedAt = profile.UpdatedAt.UTC()
	return profile, nil
}
