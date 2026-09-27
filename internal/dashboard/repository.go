package dashboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("dashboard user not found")

type Repository interface {
	Get(context.Context, uuid.UUID) (Response, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Get loads the profile and the skill progress. Both queries are queued as one
// batch so a page view costs a single round trip instead of two: on a database
// that is not in the same network the round trips dominate the latency.
func (r *PostgresRepository) Get(ctx context.Context, userID uuid.UUID) (Response, error) {
	var response Response
	batch := &pgx.Batch{}
	batch.Queue(`
		SELECT current_band::double precision, target_band::double precision, exam_date
		FROM user_profiles
		WHERE user_id = $1
	`, userID)
	batch.Queue(`
		SELECT skill, estimated_band::double precision, accuracy_percent::double precision, completed_tasks
		FROM user_skill_progress
		WHERE user_id = $1
		ORDER BY CASE skill
			WHEN 'listening' THEN 1
			WHEN 'reading' THEN 2
			WHEN 'writing' THEN 3
			WHEN 'speaking' THEN 4
		END
	`, userID)
	results := r.pool.SendBatch(ctx, batch)
	defer func() { _ = results.Close() }()

	var examDate *time.Time
	err := results.QueryRow().Scan(&response.Profile.CurrentBand, &response.Profile.TargetBand, &examDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return Response{}, ErrNotFound
	}
	if err != nil {
		return Response{}, fmt.Errorf("get dashboard profile: %w", err)
	}
	if examDate != nil {
		value := examDate.Format(time.DateOnly)
		response.Profile.ExamDate = &value
	}

	rows, err := results.Query()
	if err != nil {
		return Response{}, fmt.Errorf("get dashboard skill progress: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var progress SkillProgress
		if err := rows.Scan(
			&progress.Skill,
			&progress.EstimatedBand,
			&progress.AccuracyPercent,
			&progress.CompletedTasks,
		); err != nil {
			return Response{}, fmt.Errorf("scan dashboard skill progress: %w", err)
		}
		response.SkillProgress = append(response.SkillProgress, progress)
	}
	if err := rows.Err(); err != nil {
		return Response{}, fmt.Errorf("iterate dashboard skill progress: %w", err)
	}
	if err := results.Close(); err != nil {
		return Response{}, fmt.Errorf("close dashboard batch: %w", err)
	}
	return response, nil
}
