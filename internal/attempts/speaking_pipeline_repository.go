package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type TranscriptionWork struct {
	Recording     SpeakingRecording
	Transcription SpeakingTranscription
}

// Workers renew updated_at while an external call is running. A lost worker's
// lease expires, allowing a replacement process to resume persisted work.
func (r *PostgresRepository) RecoverSpeakingJobs(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, table := range []string{"speaking_transcriptions", "speaking_assessment_jobs"} {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET status='QUEUED',
			attempts=GREATEST(attempts-1,0), next_attempt_at=CURRENT_TIMESTAMP,
			updated_at=CURRENT_TIMESTAMP
			WHERE status='PROCESSING' AND updated_at < CURRENT_TIMESTAMP - INTERVAL '2 minutes'`); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) RenewTranscription(ctx context.Context, id uuid.UUID, revision int) error {
	_, err := r.pool.Exec(ctx, `UPDATE speaking_transcriptions SET updated_at=CURRENT_TIMESTAMP
		WHERE recording_id=$1 AND recording_revision=$2 AND status='PROCESSING'`, id, revision)
	return err
}

func (r *PostgresRepository) RenewAssessment(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE speaking_assessment_jobs SET updated_at=CURRENT_TIMESTAMP
		WHERE attempt_id=$1 AND status='PROCESSING'`, id)
	return err
}

func (r *PostgresRepository) GetSpeakingTranscription(ctx context.Context, recordingID uuid.UUID) (SpeakingTranscription, error) {
	var item SpeakingTranscription
	var words, segments, metrics []byte
	err := r.pool.QueryRow(ctx, `SELECT recording_id, recording_revision, status, provider, model,
		language, COALESCE(language_probability,0), transcript, COALESCE(audio_duration_ms,0),
		COALESCE(speech_duration_ms,0), COALESCE(processing_time_ms,0), words, segments, metrics,
		attempts, COALESCE(error_code,''), COALESCE(error_message,''), started_at, completed_at,
		created_at, updated_at FROM speaking_transcriptions WHERE recording_id=$1`, recordingID).Scan(
		&item.RecordingID, &item.RecordingRevision, &item.Status, &item.Provider, &item.Model,
		&item.Language, &item.LanguageProbability, &item.Transcript, &item.AudioDurationMS,
		&item.SpeechDurationMS, &item.ProcessingTimeMS, &words, &segments, &metrics,
		&item.Attempts, &item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt,
		&item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpeakingTranscription{}, ErrNotFound
	}
	if err != nil {
		return SpeakingTranscription{}, fmt.Errorf("get speaking transcription: %w", err)
	}
	if err := json.Unmarshal(words, &item.Words); err != nil {
		return SpeakingTranscription{}, fmt.Errorf("decode transcription words: %w", err)
	}
	if err := json.Unmarshal(segments, &item.Segments); err != nil {
		return SpeakingTranscription{}, fmt.Errorf("decode transcription segments: %w", err)
	}
	if err := json.Unmarshal(metrics, &item.Metrics); err != nil {
		return SpeakingTranscription{}, fmt.Errorf("decode transcription metrics: %w", err)
	}
	return item, nil
}

func (r *PostgresRepository) PendingTranscriptionIDs(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT recording_id FROM speaking_transcriptions
		WHERE status='QUEUED' AND attempts < 3 AND next_attempt_at <= CURRENT_TIMESTAMP
		ORDER BY next_attempt_at, created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending transcriptions: %w", err)
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

func (r *PostgresRepository) ClaimTranscription(ctx context.Context, recordingID uuid.UUID) (TranscriptionWork, bool, error) {
	command, err := r.pool.Exec(ctx, `UPDATE speaking_transcriptions SET
		status='PROCESSING', attempts=attempts+1, started_at=CURRENT_TIMESTAMP,
		error_code=NULL, error_message=NULL, updated_at=CURRENT_TIMESTAMP
		WHERE recording_id=$1 AND status='QUEUED' AND attempts < 3
		AND next_attempt_at <= CURRENT_TIMESTAMP`, recordingID)
	if err != nil {
		return TranscriptionWork{}, false, fmt.Errorf("claim transcription: %w", err)
	}
	if command.RowsAffected() == 0 {
		return TranscriptionWork{}, false, nil
	}
	var work TranscriptionWork
	err = r.pool.QueryRow(ctx, `SELECT id, attempt_id, part_id, original_name, mime_type,
		storage_key, byte_size, revision, created_at, updated_at
		FROM speaking_recordings WHERE id=$1`, recordingID).Scan(
		&work.Recording.ID, &work.Recording.AttemptID, &work.Recording.PartID,
		&work.Recording.OriginalName, &work.Recording.MimeType, &work.Recording.StorageKey,
		&work.Recording.ByteSize, &work.Recording.Revision, &work.Recording.CreatedAt,
		&work.Recording.UpdatedAt)
	if err != nil {
		return TranscriptionWork{}, false, fmt.Errorf("load claimed recording: %w", err)
	}
	work.Transcription, err = r.GetSpeakingTranscription(ctx, recordingID)
	return work, true, err
}

func (r *PostgresRepository) CompleteTranscription(ctx context.Context, result SpeakingTranscription) (bool, error) {
	words, _ := json.Marshal(result.Words)
	segments, _ := json.Marshal(result.Segments)
	metrics, _ := json.Marshal(result.Metrics)
	command, err := r.pool.Exec(ctx, `UPDATE speaking_transcriptions SET
		status='READY', provider=$3, model=$4, language=$5, language_probability=$6,
		transcript=$7, audio_duration_ms=$8, speech_duration_ms=$9, processing_time_ms=$10,
		words=$11::jsonb, segments=$12::jsonb, metrics=$13::jsonb, completed_at=CURRENT_TIMESTAMP,
		error_code=NULL, error_message=NULL, updated_at=CURRENT_TIMESTAMP
		WHERE recording_id=$1 AND recording_revision=$2 AND status='PROCESSING'`,
		result.RecordingID, result.RecordingRevision, result.Provider, result.Model, result.Language,
		result.LanguageProbability, result.Transcript, result.AudioDurationMS, result.SpeechDurationMS,
		result.ProcessingTimeMS, words, segments, metrics)
	if err != nil {
		return false, fmt.Errorf("complete transcription: %w", err)
	}
	return command.RowsAffected() == 1, nil
}

func (r *PostgresRepository) FailTranscription(ctx context.Context, recordingID uuid.UUID, revision int, message string) error {
	_, err := r.pool.Exec(ctx, `UPDATE speaking_transcriptions SET
		status=CASE WHEN attempts >= 3 THEN 'FAILED' ELSE 'QUEUED' END,
		next_attempt_at=CURRENT_TIMESTAMP + CASE WHEN attempts <= 1 THEN INTERVAL '10 seconds' ELSE INTERVAL '60 seconds' END,
		error_code='TRANSCRIPTION_FAILED', error_message=$3, updated_at=CURRENT_TIMESTAMP
		WHERE recording_id=$1 AND recording_revision=$2 AND status='PROCESSING'`, recordingID, revision, message)
	return err
}

func (r *PostgresRepository) QueueSpeakingAssessment(ctx context.Context, attemptID uuid.UUID, answers []AnswerInput) error {
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
	if _, err := tx.Exec(ctx, `INSERT INTO speaking_assessment_jobs (attempt_id, status)
		VALUES ($1,'QUEUED') ON CONFLICT (attempt_id) DO UPDATE SET status='QUEUED', attempts=0,
		next_attempt_at=CURRENT_TIMESTAMP, error_code=NULL, error_message=NULL,
		started_at=NULL, completed_at=NULL, updated_at=CURRENT_TIMESTAMP`, attemptID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) PendingAssessmentIDs(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT attempt_id FROM speaking_assessment_jobs
		WHERE status='QUEUED' AND attempts < 3 AND next_attempt_at <= CURRENT_TIMESTAMP
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

func (r *PostgresRepository) GetSpeakingAssessmentJob(ctx context.Context, attemptID uuid.UUID) (SpeakingAssessmentJob, error) {
	var item SpeakingAssessmentJob
	err := r.pool.QueryRow(ctx, `SELECT status, attempts, COALESCE(error_code,''),
		COALESCE(error_message,'') FROM speaking_assessment_jobs WHERE attempt_id=$1`, attemptID).
		Scan(&item.Status, &item.Attempts, &item.ErrorCode, &item.ErrorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpeakingAssessmentJob{}, ErrNotFound
	}
	if err != nil {
		return SpeakingAssessmentJob{}, fmt.Errorf("get speaking assessment job: %w", err)
	}
	return item, nil
}

func (r *PostgresRepository) ClaimAssessment(ctx context.Context, attemptID uuid.UUID) (bool, error) {
	command, err := r.pool.Exec(ctx, `UPDATE speaking_assessment_jobs SET status='PROCESSING',
		attempts=attempts+1, started_at=CURRENT_TIMESTAMP, error_code=NULL, error_message=NULL,
		updated_at=CURRENT_TIMESTAMP WHERE attempt_id=$1 AND status='QUEUED'
		AND attempts < 3 AND next_attempt_at <= CURRENT_TIMESTAMP`, attemptID)
	return command.RowsAffected() == 1, err
}

func (r *PostgresRepository) CompleteAssessment(ctx context.Context, attemptID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE speaking_assessment_jobs SET status='READY',
		completed_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE attempt_id=$1`, attemptID)
	return err
}

func (r *PostgresRepository) FailAssessment(ctx context.Context, attemptID uuid.UUID, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE speaking_assessment_jobs SET
		status=CASE WHEN attempts >= 3 THEN 'FAILED' ELSE 'QUEUED' END,
		next_attempt_at=CURRENT_TIMESTAMP + CASE WHEN attempts <= 1 THEN INTERVAL '10 seconds' ELSE INTERVAL '60 seconds' END,
		error_code='ASSESSMENT_FAILED', error_message=$2, updated_at=CURRENT_TIMESTAMP
		WHERE attempt_id=$1 AND status='PROCESSING'`, attemptID, message); err != nil {
		return err
	}
	// A terminal worker failure must not strand the student in PROCESSING.
	// Returning the attempt to IN_PROGRESS lets a practice attempt be reopened
	// and resubmitted; Full Mock users can reopen the same current section.
	if _, err := tx.Exec(ctx, `UPDATE attempts SET status='IN_PROGRESS'
		WHERE id=$1 AND status='PROCESSING' AND EXISTS (
			SELECT 1 FROM speaking_assessment_jobs
			WHERE attempt_id=$1 AND status='FAILED'
		)`, attemptID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) DeferAssessment(ctx context.Context, attemptID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE speaking_assessment_jobs SET status='QUEUED',
		attempts=GREATEST(attempts-1,0), next_attempt_at=CURRENT_TIMESTAMP + INTERVAL '3 seconds',
		updated_at=CURRENT_TIMESTAMP WHERE attempt_id=$1 AND status='PROCESSING'`, attemptID)
	return err
}
