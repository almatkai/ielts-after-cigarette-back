package attempts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StatusDetail is the small, consistent snapshot used while waiting for AI.
// It deliberately excludes answers, recordings, transcripts and evaluations.
type StatusDetail struct {
	ID                 uuid.UUID              `json:"id"`
	Status             string                 `json:"status"`
	SpeakingAssessment *SpeakingAssessmentJob `json:"speakingAssessment,omitempty"`
	WritingAssessment  *WritingAssessmentJob  `json:"writingAssessment,omitempty"`
}

func (s *Service) Status(ctx context.Context, userID, attemptID uuid.UUID) (StatusDetail, error) {
	return s.repository.GetStatus(ctx, userID, attemptID)
}

// GetStatus checks ownership and reads attempt/job state in one statement, so
// a completed/failed worker cannot leave the response mixing two snapshots.
func (r *PostgresRepository) GetStatus(ctx context.Context, userID, attemptID uuid.UUID) (StatusDetail, error) {
	var result StatusDetail
	var speakingStatus, writingStatus *string
	var speaking SpeakingAssessmentJob
	var writing WritingAssessmentJob
	err := r.pool.QueryRow(ctx, `
		SELECT a.id, a.status,
			s.status, COALESCE(s.attempts,0), COALESCE(s.error_code,''), COALESCE(s.error_message,''),
			w.status, COALESCE(w.attempts,0), COALESCE(w.error_code,''), COALESCE(w.error_message,'')
		FROM attempts a
		LEFT JOIN speaking_assessment_jobs s ON s.attempt_id = a.id
			AND a.material_type = 'speaking' AND a.status IN ('IN_PROGRESS','PROCESSING')
		LEFT JOIN writing_assessment_jobs w ON w.attempt_id = a.id
			AND a.material_type = 'writing' AND a.status IN ('IN_PROGRESS','PROCESSING')
		WHERE a.id = $1 AND a.user_id = $2
	`, attemptID, userID).Scan(&result.ID, &result.Status,
		&speakingStatus, &speaking.Attempts, &speaking.ErrorCode, &speaking.ErrorMessage,
		&writingStatus, &writing.Attempts, &writing.ErrorCode, &writing.ErrorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return StatusDetail{}, ErrNotFound
	}
	if err != nil {
		return StatusDetail{}, fmt.Errorf("get attempt status: %w", err)
	}
	if speakingStatus != nil {
		speaking.Status = *speakingStatus
		result.SpeakingAssessment = &speaking
	}
	if writingStatus != nil {
		writing.Status = *writingStatus
		result.WritingAssessment = &writing
	}
	return result, nil
}
