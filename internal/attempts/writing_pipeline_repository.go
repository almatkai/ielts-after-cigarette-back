package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) QueueWritingAssessment(ctx context.Context, attemptID uuid.UUID, answers []AnswerInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM attempts WHERE id=$1 FOR UPDATE`, attemptID).Scan(&status); err != nil {
		return err
	}
	if status != StatusInProgress {
		return ErrAlreadySubmitted
	}
	for _, item := range answers {
		value, _ := json.Marshal(item.Answer)
		if _, err := tx.Exec(ctx, `INSERT INTO attempt_answers (attempt_id, question_id, answer)
			VALUES ($1,$2,$3::jsonb) ON CONFLICT (attempt_id, question_id)
			DO UPDATE SET answer=EXCLUDED.answer`, attemptID, item.QuestionID, value); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE attempts SET status='PROCESSING' WHERE id=$1`, attemptID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO writing_assessment_jobs (attempt_id, status)
		VALUES ($1,'QUEUED') ON CONFLICT (attempt_id) DO UPDATE SET status='QUEUED', attempts=0,
		next_attempt_at=CURRENT_TIMESTAMP, error_code=NULL, error_message=NULL,
		started_at=NULL, completed_at=NULL, updated_at=CURRENT_TIMESTAMP`, attemptID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PendingWritingAssessmentIDs returns QUEUED jobs that are ready to be (re)queued
// into the worker queue. A grace period on updated_at keeps the 5-second
// recoverPending tick from re-pushing attempt IDs that are still sitting in
// Redis behind a slow LLM call. updated_at (not created_at) is the right
// column: it is refreshed on both enqueue and failure, so retried jobs with
// backoff are still picked up promptly.
func (r *PostgresRepository) PendingWritingAssessmentIDs(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT attempt_id FROM writing_assessment_jobs
		WHERE status='QUEUED' AND attempts < 3 AND next_attempt_at <= CURRENT_TIMESTAMP
		AND updated_at < CURRENT_TIMESTAMP - INTERVAL '30 seconds'
		ORDER BY next_attempt_at, created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *PostgresRepository) GetWritingAssessmentJob(ctx context.Context, attemptID uuid.UUID) (WritingAssessmentJob, error) {
	var item WritingAssessmentJob
	err := r.pool.QueryRow(ctx, `SELECT status, attempts, COALESCE(error_code,''),
		COALESCE(error_message,'') FROM writing_assessment_jobs WHERE attempt_id=$1`, attemptID).
		Scan(&item.Status, &item.Attempts, &item.ErrorCode, &item.ErrorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return WritingAssessmentJob{}, ErrNotFound
	}
	if err != nil {
		return WritingAssessmentJob{}, fmt.Errorf("get writing assessment job: %w", err)
	}
	return item, nil
}

func (r *PostgresRepository) ClaimWritingAssessment(ctx context.Context, attemptID uuid.UUID) (bool, error) {
	command, err := r.pool.Exec(ctx, `UPDATE writing_assessment_jobs SET status='PROCESSING',
		attempts=attempts+1, started_at=CURRENT_TIMESTAMP, error_code=NULL, error_message=NULL,
		updated_at=CURRENT_TIMESTAMP WHERE attempt_id=$1 AND status='QUEUED'
		AND attempts < 3 AND next_attempt_at <= CURRENT_TIMESTAMP`, attemptID)
	return command.RowsAffected() == 1, err
}

func (r *PostgresRepository) RenewWritingAssessment(ctx context.Context, attemptID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE writing_assessment_jobs SET updated_at=CURRENT_TIMESTAMP
		WHERE attempt_id=$1 AND status='PROCESSING'`, attemptID)
	return err
}

func (r *PostgresRepository) CompleteWritingAssessment(ctx context.Context, attemptID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE writing_assessment_jobs SET status='READY',
		completed_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE attempt_id=$1`, attemptID)
	return err
}

func (r *PostgresRepository) FailWritingAssessment(ctx context.Context, attemptID uuid.UUID, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE writing_assessment_jobs SET
		status=CASE WHEN attempts >= 3 THEN 'FAILED' ELSE 'QUEUED' END,
		next_attempt_at=CURRENT_TIMESTAMP + CASE WHEN attempts <= 1 THEN INTERVAL '10 seconds' ELSE INTERVAL '60 seconds' END,
		error_code='ASSESSMENT_FAILED', error_message=$2, updated_at=CURRENT_TIMESTAMP
		WHERE attempt_id=$1 AND status='PROCESSING'`, attemptID, message); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE attempts SET status='IN_PROGRESS'
		WHERE id=$1 AND status='PROCESSING' AND EXISTS (
			SELECT 1 FROM writing_assessment_jobs
			WHERE attempt_id=$1 AND status='FAILED'
		)`, attemptID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) RecoverWritingJobs(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `UPDATE writing_assessment_jobs SET status='QUEUED',
		attempts=GREATEST(attempts-1,0), next_attempt_at=CURRENT_TIMESTAMP,
		updated_at=CURRENT_TIMESTAMP
		WHERE status='PROCESSING' AND updated_at < CURRENT_TIMESTAMP - INTERVAL '2 minutes'`)
	return err
}
