package attempts

import (
	"context"

	"github.com/google/uuid"
)

// RetryAssessment reuses the already persisted submission, even when a Full
// Mock has advanced beyond it. It never accepts new answers or resets a timer.
func (s *Service) RetryAssessment(ctx context.Context, userID, attemptID uuid.UUID) (Attempt, error) {
	attempt, err := s.own(ctx, userID, attemptID)
	if err != nil {
		return Attempt{}, err
	}
	repository, ok := s.repository.(interface {
		RetryAssessment(context.Context, uuid.UUID, string) error
	})
	if !ok {
		return Attempt{}, ErrAIUnavailable
	}
	if err := repository.RetryAssessment(ctx, attempt.ID, attempt.MaterialType); err != nil {
		return Attempt{}, err
	}
	return s.repository.Get(ctx, attempt.ID)
}

func (r *PostgresRepository) RetryAssessment(ctx context.Context, id uuid.UUID, skill string) error {
	table := "writing_assessment_jobs"
	if skill == MaterialSpeaking {
		table = "speaking_assessment_jobs"
	} else if skill != MaterialWriting {
		return ErrAssessmentRetryNotAllowed
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM attempts WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		return err
	}
	if status != StatusInProgress {
		return ErrAssessmentRetryNotAllowed
	}
	command, err := tx.Exec(ctx, `UPDATE `+table+` SET status='QUEUED', attempts=0,
		next_attempt_at=CURRENT_TIMESTAMP, error_code=NULL, error_message=NULL,
		started_at=NULL, completed_at=NULL, updated_at=CURRENT_TIMESTAMP
		WHERE attempt_id=$1 AND status='FAILED'`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrAssessmentRetryNotAllowed
	}
	if skill == MaterialSpeaking {
		if _, err := tx.Exec(ctx, `UPDATE speaking_transcriptions SET status='QUEUED',attempts=0,
			next_attempt_at=CURRENT_TIMESTAMP,error_code=NULL,error_message=NULL,
			started_at=NULL,completed_at=NULL,updated_at=CURRENT_TIMESTAMP
			WHERE status='FAILED' AND recording_id IN (SELECT id FROM speaking_recordings WHERE attempt_id=$1)`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE attempts SET status='PROCESSING' WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
