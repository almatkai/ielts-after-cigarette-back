package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const attemptColumns = `id, user_id, material_type, material_id, material_version_id,
	status, score, max_score, band::double precision, started_at, submitted_at`

type SubmitResult struct {
	AttemptID uuid.UUID
	UserID    uuid.UUID
	Skill     string
	Answers   []Answer
	Score     int
	MaxScore  int
	Band      float64
	Accuracy  float64
}

type Repository interface {
	FindInProgress(context.Context, uuid.UUID, string, uuid.UUID) (Attempt, error)
	Create(context.Context, Attempt) (Attempt, error)
	Get(context.Context, uuid.UUID) (Attempt, error)
	ListByUser(context.Context, uuid.UUID, string) ([]Summary, error)
	SaveAnswers(context.Context, uuid.UUID, []AnswerInput) error
	ListAnswers(context.Context, uuid.UUID) ([]Answer, error)
	Submit(context.Context, SubmitResult) error
}

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) FindInProgress(ctx context.Context, userID uuid.UUID, materialType string, materialID uuid.UUID) (Attempt, error) {
	var attempt Attempt
	err := r.pool.QueryRow(ctx, `SELECT `+attemptColumns+` FROM attempts
		WHERE user_id=$1 AND material_type=$2 AND material_id=$3 AND status='IN_PROGRESS'
		ORDER BY started_at DESC LIMIT 1`, userID, materialType, materialID).Scan(
		&attempt.ID, &attempt.UserID, &attempt.MaterialType, &attempt.MaterialID,
		&attempt.MaterialVersionID, &attempt.Status, &attempt.Score, &attempt.MaxScore,
		&attempt.Band, &attempt.StartedAt, &attempt.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, ErrNotFound
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("find in-progress attempt: %w", err)
	}
	return attempt, nil
}

func (r *PostgresRepository) Create(ctx context.Context, attempt Attempt) (Attempt, error) {
	err := r.pool.QueryRow(ctx, `INSERT INTO attempts
		(id, user_id, material_type, material_id, material_version_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING `+attemptColumns,
		attempt.ID, attempt.UserID, attempt.MaterialType, attempt.MaterialID, attempt.MaterialVersionID).Scan(
		&attempt.ID, &attempt.UserID, &attempt.MaterialType, &attempt.MaterialID,
		&attempt.MaterialVersionID, &attempt.Status, &attempt.Score, &attempt.MaxScore,
		&attempt.Band, &attempt.StartedAt, &attempt.SubmittedAt)
	if err != nil {
		return Attempt{}, fmt.Errorf("create attempt: %w", err)
	}
	return attempt, nil
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Attempt, error) {
	var attempt Attempt
	err := r.pool.QueryRow(ctx, `SELECT `+attemptColumns+` FROM attempts WHERE id=$1`, id).Scan(
		&attempt.ID, &attempt.UserID, &attempt.MaterialType, &attempt.MaterialID,
		&attempt.MaterialVersionID, &attempt.Status, &attempt.Score, &attempt.MaxScore,
		&attempt.Band, &attempt.StartedAt, &attempt.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, ErrNotFound
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("get attempt: %w", err)
	}
	return attempt, nil
}

func (r *PostgresRepository) ListByUser(ctx context.Context, userID uuid.UUID, materialType string) ([]Summary, error) {
	rows, err := r.pool.Query(ctx, `SELECT a.id, a.user_id, a.material_type, a.material_id,
		a.material_version_id, a.status, a.score, a.max_score, a.band::double precision,
		a.started_at, a.submitted_at,
		COALESCE(lv.title, rv.title, ''), COALESCE(lt.slug, rm.slug, '')
		FROM attempts a
		LEFT JOIN listening_tests lt ON lt.id = a.material_id AND a.material_type = 'listening'
		LEFT JOIN listening_test_versions lv ON lv.id = a.material_version_id AND a.material_type = 'listening'
		LEFT JOIN reading_materials rm ON rm.id = a.material_id AND a.material_type = 'reading'
		LEFT JOIN reading_material_versions rv ON rv.id = a.material_version_id AND a.material_type = 'reading'
		WHERE a.user_id = $1 AND ($2 = '' OR a.material_type = $2)
		ORDER BY a.started_at DESC`, userID, materialType)
	if err != nil {
		return nil, fmt.Errorf("list attempts: %w", err)
	}
	defer rows.Close()
	items := []Summary{}
	for rows.Next() {
		var item Summary
		if err := rows.Scan(&item.ID, &item.UserID, &item.MaterialType, &item.MaterialID,
			&item.MaterialVersionID, &item.Status, &item.Score, &item.MaxScore,
			&item.Band, &item.StartedAt, &item.SubmittedAt,
			&item.TestTitle, &item.TestSlug); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) SaveAnswers(ctx context.Context, attemptID uuid.UUID, answers []AnswerInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, item := range answers {
		answer, _ := json.Marshal(item.Answer)
		if _, err := tx.Exec(ctx, `INSERT INTO attempt_answers (attempt_id, question_id, answer)
			VALUES ($1,$2,$3::jsonb)
			ON CONFLICT (attempt_id, question_id) DO UPDATE SET answer = EXCLUDED.answer`,
			attemptID, item.QuestionID, answer); err != nil {
			return fmt.Errorf("save attempt answer: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) ListAnswers(ctx context.Context, attemptID uuid.UUID) ([]Answer, error) {
	rows, err := r.pool.Query(ctx, `SELECT question_id, answer, is_correct, points_awarded
		FROM attempt_answers WHERE attempt_id=$1 ORDER BY question_id`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("list attempt answers: %w", err)
	}
	defer rows.Close()
	items := []Answer{}
	for rows.Next() {
		var item Answer
		var raw []byte
		if err := rows.Scan(&item.QuestionID, &raw, &item.IsCorrect, &item.PointsAwarded); err != nil {
			return nil, fmt.Errorf("scan attempt answer: %w", err)
		}
		_ = json.Unmarshal(raw, &item.Answer)
		if item.Answer == nil {
			item.Answer = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) Submit(ctx context.Context, result SubmitResult) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, item := range result.Answers {
		answer, _ := json.Marshal(item.Answer)
		if _, err := tx.Exec(ctx, `INSERT INTO attempt_answers
			(attempt_id, question_id, answer, is_correct, points_awarded)
			VALUES ($1,$2,$3::jsonb,$4,$5)
			ON CONFLICT (attempt_id, question_id) DO UPDATE SET
				answer = EXCLUDED.answer,
				is_correct = EXCLUDED.is_correct,
				points_awarded = EXCLUDED.points_awarded`,
			result.AttemptID, item.QuestionID, answer, item.IsCorrect, item.PointsAwarded); err != nil {
			return fmt.Errorf("save graded answer: %w", err)
		}
	}
	command, err := tx.Exec(ctx, `UPDATE attempts SET status='SUBMITTED',
		score=$2, max_score=$3, band=$4, submitted_at=CURRENT_TIMESTAMP
		WHERE id=$1 AND status='IN_PROGRESS'`,
		result.AttemptID, result.Score, result.MaxScore, result.Band)
	if err != nil {
		return fmt.Errorf("submit attempt: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAlreadySubmitted
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_skill_progress
		(id, user_id, skill, estimated_band, accuracy_percent, completed_tasks)
		VALUES ($1,$2,$3,$4,$5,1)
		ON CONFLICT (user_id, skill) DO UPDATE SET
			estimated_band = EXCLUDED.estimated_band,
			accuracy_percent = EXCLUDED.accuracy_percent,
			completed_tasks = user_skill_progress.completed_tasks + 1`,
		uuid.New(), result.UserID, result.Skill, result.Band, result.Accuracy); err != nil {
		return fmt.Errorf("update skill progress: %w", err)
	}
	return tx.Commit(ctx)
}
