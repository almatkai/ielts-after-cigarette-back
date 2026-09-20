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
	AttemptID          uuid.UUID
	UserID             uuid.UUID
	Skill              string
	Answers            []Answer
	Score              int
	MaxScore           int
	Band               float64
	Accuracy           float64
	WritingEvaluation  *WritingEvaluation
	SpeakingEvaluation *SpeakingEvaluation
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
		WHERE user_id=$1 AND material_type=$2 AND material_id=$3 AND status IN ('IN_PROGRESS','PROCESSING')
		AND NOT EXISTS (
			SELECT 1 FROM full_mock_session_sections fmss WHERE fmss.attempt_id = attempts.id
		)
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
		COALESCE(lv.title, rv.title, wv.title, sv.title, ''), COALESCE(lt.slug, rm.slug, wm.slug, sm.slug, '')
		FROM attempts a
		LEFT JOIN listening_tests lt ON lt.id = a.material_id AND a.material_type = 'listening'
		LEFT JOIN listening_test_versions lv ON lv.id = a.material_version_id AND a.material_type = 'listening'
		LEFT JOIN reading_materials rm ON rm.id = a.material_id AND a.material_type = 'reading'
		LEFT JOIN reading_material_versions rv ON rv.id = a.material_version_id AND a.material_type = 'reading'
		LEFT JOIN writing_materials wm ON wm.id = a.material_id AND a.material_type = 'writing'
		LEFT JOIN writing_material_versions wv ON wv.id = a.material_version_id AND a.material_type = 'writing'
		LEFT JOIN speaking_materials sm ON sm.id = a.material_id AND a.material_type = 'speaking'
		LEFT JOIN speaking_material_versions sv ON sv.id = a.material_version_id AND a.material_type = 'speaking'
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

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM attempts WHERE id=$1 FOR UPDATE`, attemptID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock attempt for saving answers: %w", err)
	}
	if status != StatusInProgress {
		return ErrAlreadySubmitted
	}

	for _, item := range answers {
		answer, err := json.Marshal(item.Answer)
		if err != nil {
			return fmt.Errorf("marshal answer: %w", err)
		}
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

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM attempts WHERE id=$1 FOR UPDATE`, result.AttemptID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock attempt for submit: %w", err)
	}
	if status != StatusInProgress && status != StatusProcessing {
		return ErrAlreadySubmitted
	}

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
		WHERE id=$1 AND status IN ('IN_PROGRESS','PROCESSING')`,
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
	if result.WritingEvaluation != nil {
		feedback, err := json.Marshal(struct {
			Summary  string                `json:"summary"`
			Criteria WritingCriteria       `json:"criteria"`
			Tasks    []WritingTaskFeedback `json:"tasks"`
		}{
			Summary: result.WritingEvaluation.Summary, Criteria: result.WritingEvaluation.Criteria,
			Tasks: result.WritingEvaluation.Tasks,
		})
		if err != nil {
			return fmt.Errorf("encode writing evaluation: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO writing_evaluations
			(attempt_id, model, overall_band, task_response_band, coherence_band, lexical_resource_band, grammar_band, feedback)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`,
			result.AttemptID, result.WritingEvaluation.Model, result.WritingEvaluation.OverallBand,
			result.WritingEvaluation.Criteria.TaskResponse.Band, result.WritingEvaluation.Criteria.Coherence.Band,
			result.WritingEvaluation.Criteria.LexicalResource.Band, result.WritingEvaluation.Criteria.Grammar.Band,
			feedback); err != nil {
			return fmt.Errorf("save writing evaluation: %w", err)
		}
	}
	if result.SpeakingEvaluation != nil {
		feedback, err := json.Marshal(struct {
			Summary                string                 `json:"summary"`
			Criteria               SpeakingCriteria       `json:"criteria"`
			Parts                  []SpeakingPartFeedback `json:"parts"`
			PronunciationAvailable bool                   `json:"pronunciationAvailable"`
		}{
			Summary: result.SpeakingEvaluation.Summary, Criteria: result.SpeakingEvaluation.Criteria,
			Parts: result.SpeakingEvaluation.Parts, PronunciationAvailable: result.SpeakingEvaluation.PronunciationAvailable,
		})
		if err != nil {
			return fmt.Errorf("encode speaking evaluation: %w", err)
		}
		var pronunciationBand any
		if result.SpeakingEvaluation.PronunciationAvailable {
			pronunciationBand = result.SpeakingEvaluation.Criteria.Pronunciation.Band
		}
		if _, err := tx.Exec(ctx, `INSERT INTO speaking_evaluations
			(attempt_id, model, overall_band, fluency_band, lexical_resource_band, grammar_band, pronunciation_band, feedback)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`,
			result.AttemptID, result.SpeakingEvaluation.Model, result.SpeakingEvaluation.OverallBand,
			result.SpeakingEvaluation.Criteria.Fluency.Band, result.SpeakingEvaluation.Criteria.LexicalResource.Band,
			result.SpeakingEvaluation.Criteria.Grammar.Band, pronunciationBand,
			feedback); err != nil {
			return fmt.Errorf("save speaking evaluation: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) GetWritingEvaluation(ctx context.Context, attemptID uuid.UUID) (WritingEvaluation, error) {
	var evaluation WritingEvaluation
	var feedback []byte
	err := r.pool.QueryRow(ctx, `SELECT model, overall_band::double precision,
		task_response_band::double precision, coherence_band::double precision,
		lexical_resource_band::double precision, grammar_band::double precision,
		feedback, evaluated_at
		FROM writing_evaluations WHERE attempt_id=$1`, attemptID).Scan(
		&evaluation.Model, &evaluation.OverallBand, &evaluation.Criteria.TaskResponse.Band,
		&evaluation.Criteria.Coherence.Band, &evaluation.Criteria.LexicalResource.Band,
		&evaluation.Criteria.Grammar.Band, &feedback, &evaluation.EvaluatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WritingEvaluation{}, ErrNotFound
	}
	if err != nil {
		return WritingEvaluation{}, fmt.Errorf("get writing evaluation: %w", err)
	}
	var stored struct {
		Summary  string                `json:"summary"`
		Criteria WritingCriteria       `json:"criteria"`
		Tasks    []WritingTaskFeedback `json:"tasks"`
	}
	if err := json.Unmarshal(feedback, &stored); err != nil {
		return WritingEvaluation{}, fmt.Errorf("decode writing evaluation: %w", err)
	}
	evaluation.AttemptID = attemptID
	evaluation.Summary = stored.Summary
	evaluation.Tasks = stored.Tasks
	evaluation.Criteria.TaskResponse.Feedback = stored.Criteria.TaskResponse.Feedback
	evaluation.Criteria.Coherence.Feedback = stored.Criteria.Coherence.Feedback
	evaluation.Criteria.LexicalResource.Feedback = stored.Criteria.LexicalResource.Feedback
	evaluation.Criteria.Grammar.Feedback = stored.Criteria.Grammar.Feedback
	return evaluation, nil
}

func (r *PostgresRepository) GetSpeakingEvaluation(ctx context.Context, attemptID uuid.UUID) (SpeakingEvaluation, error) {
	var evaluation SpeakingEvaluation
	var feedback []byte
	var pronunciationBand *float64
	err := r.pool.QueryRow(ctx, `SELECT model, overall_band::double precision,
		fluency_band::double precision, lexical_resource_band::double precision,
		grammar_band::double precision, pronunciation_band::double precision,
		feedback, evaluated_at
		FROM speaking_evaluations WHERE attempt_id=$1`, attemptID).Scan(
		&evaluation.Model, &evaluation.OverallBand, &evaluation.Criteria.Fluency.Band,
		&evaluation.Criteria.LexicalResource.Band, &evaluation.Criteria.Grammar.Band,
		&pronunciationBand, &feedback, &evaluation.EvaluatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpeakingEvaluation{}, ErrNotFound
	}
	if err != nil {
		return SpeakingEvaluation{}, fmt.Errorf("get speaking evaluation: %w", err)
	}
	var stored struct {
		Summary                string                 `json:"summary"`
		Criteria               SpeakingCriteria       `json:"criteria"`
		Parts                  []SpeakingPartFeedback `json:"parts"`
		PronunciationAvailable bool                   `json:"pronunciationAvailable"`
	}
	if err := json.Unmarshal(feedback, &stored); err != nil {
		return SpeakingEvaluation{}, fmt.Errorf("decode speaking evaluation: %w", err)
	}
	evaluation.AttemptID = attemptID
	evaluation.Summary = stored.Summary
	evaluation.Parts = stored.Parts
	evaluation.Criteria.Fluency.Feedback = stored.Criteria.Fluency.Feedback
	evaluation.Criteria.LexicalResource.Feedback = stored.Criteria.LexicalResource.Feedback
	evaluation.Criteria.Grammar.Feedback = stored.Criteria.Grammar.Feedback
	evaluation.Criteria.Pronunciation.Feedback = stored.Criteria.Pronunciation.Feedback
	evaluation.PronunciationAvailable = stored.PronunciationAvailable && pronunciationBand != nil
	if pronunciationBand != nil {
		evaluation.Criteria.Pronunciation.Band = *pronunciationBand
	}
	return evaluation, nil
}

func (r *PostgresRepository) UpsertSpeakingRecording(ctx context.Context, recording SpeakingRecording) (SpeakingRecording, string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return SpeakingRecording{}, "", err
	}
	defer tx.Rollback(ctx)
	var previousKey string
	var existingID uuid.UUID
	var existingRevision int
	err = tx.QueryRow(ctx, `SELECT id, storage_key, revision FROM speaking_recordings
		WHERE attempt_id=$1 AND part_id=$2 FOR UPDATE`, recording.AttemptID, recording.PartID).
		Scan(&existingID, &previousKey, &existingRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SpeakingRecording{}, "", fmt.Errorf("find existing speaking recording: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		recording.Revision = 1
		err = tx.QueryRow(ctx, `INSERT INTO speaking_recordings
			(id, attempt_id, part_id, original_name, mime_type, storage_key, byte_size, revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING id, revision, created_at, updated_at`,
			recording.ID, recording.AttemptID, recording.PartID, recording.OriginalName,
			recording.MimeType, recording.StorageKey, recording.ByteSize, recording.Revision).Scan(
			&recording.ID, &recording.Revision, &recording.CreatedAt, &recording.UpdatedAt)
	} else {
		recording.ID = existingID
		recording.Revision = existingRevision + 1
		err = tx.QueryRow(ctx, `UPDATE speaking_recordings SET
			original_name=$2, mime_type=$3, storage_key=$4, byte_size=$5,
			revision=$6, updated_at=CURRENT_TIMESTAMP WHERE id=$1
			RETURNING id, revision, created_at, updated_at`, recording.ID, recording.OriginalName,
			recording.MimeType, recording.StorageKey, recording.ByteSize, recording.Revision).Scan(
			&recording.ID, &recording.Revision, &recording.CreatedAt, &recording.UpdatedAt)
	}
	if err != nil {
		return SpeakingRecording{}, "", fmt.Errorf("save speaking recording: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO speaking_transcriptions
		(recording_id, recording_revision, status, attempts, next_attempt_at)
		VALUES ($1,$2,'QUEUED',0,CURRENT_TIMESTAMP)
		ON CONFLICT (recording_id) DO UPDATE SET
			recording_revision=EXCLUDED.recording_revision, status='QUEUED', provider='faster-whisper',
			model='', language='en', language_probability=NULL, transcript='', audio_duration_ms=NULL,
			speech_duration_ms=NULL, processing_time_ms=NULL, words='[]'::jsonb, segments='[]'::jsonb,
			metrics='{}'::jsonb, attempts=0, next_attempt_at=CURRENT_TIMESTAMP,
			error_code=NULL, error_message=NULL, started_at=NULL, completed_at=NULL,
			updated_at=CURRENT_TIMESTAMP`, recording.ID, recording.Revision); err != nil {
		return SpeakingRecording{}, "", fmt.Errorf("queue speaking transcription: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SpeakingRecording{}, "", fmt.Errorf("commit speaking recording: %w", err)
	}
	return recording, previousKey, nil
}

func (r *PostgresRepository) ListSpeakingRecordings(ctx context.Context, attemptID uuid.UUID) ([]SpeakingRecording, error) {
	rows, err := r.pool.Query(ctx, `SELECT
		r.id, r.attempt_id, r.part_id, r.original_name, r.mime_type,
		r.storage_key, r.byte_size, r.revision, r.created_at, r.updated_at,
		t.recording_id IS NOT NULL,
		COALESCE(t.recording_revision,0), COALESCE(t.status,''), COALESCE(t.provider,''),
		COALESCE(t.model,''), COALESCE(t.language,''), COALESCE(t.language_probability,0),
		COALESCE(t.transcript,''), COALESCE(t.audio_duration_ms,0),
		COALESCE(t.speech_duration_ms,0), COALESCE(t.processing_time_ms,0),
		COALESCE(t.words,'[]'::jsonb), COALESCE(t.segments,'[]'::jsonb),
		COALESCE(t.metrics,'{}'::jsonb), COALESCE(t.attempts,0),
		COALESCE(t.error_code,''), COALESCE(t.error_message,''), t.started_at, t.completed_at,
		COALESCE(t.created_at,r.created_at), COALESCE(t.updated_at,r.updated_at)
		FROM speaking_recordings r
		LEFT JOIN speaking_transcriptions t ON t.recording_id=r.id
		WHERE r.attempt_id=$1 ORDER BY r.created_at`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("list speaking recordings: %w", err)
	}
	defer rows.Close()
	items := []SpeakingRecording{}
	for rows.Next() {
		var item SpeakingRecording
		var hasTranscription bool
		var transcription SpeakingTranscription
		var words, segments, metrics []byte
		if err := rows.Scan(&item.ID, &item.AttemptID, &item.PartID, &item.OriginalName,
			&item.MimeType, &item.StorageKey, &item.ByteSize, &item.Revision, &item.CreatedAt, &item.UpdatedAt,
			&hasTranscription, &transcription.RecordingRevision, &transcription.Status,
			&transcription.Provider, &transcription.Model, &transcription.Language,
			&transcription.LanguageProbability, &transcription.Transcript,
			&transcription.AudioDurationMS, &transcription.SpeechDurationMS,
			&transcription.ProcessingTimeMS, &words, &segments, &metrics,
			&transcription.Attempts, &transcription.ErrorCode, &transcription.ErrorMessage,
			&transcription.StartedAt, &transcription.CompletedAt, &transcription.CreatedAt,
			&transcription.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan speaking recording: %w", err)
		}
		if hasTranscription {
			transcription.RecordingID = item.ID
			if err := json.Unmarshal(words, &transcription.Words); err != nil {
				return nil, fmt.Errorf("decode transcription words: %w", err)
			}
			if err := json.Unmarshal(segments, &transcription.Segments); err != nil {
				return nil, fmt.Errorf("decode transcription segments: %w", err)
			}
			if err := json.Unmarshal(metrics, &transcription.Metrics); err != nil {
				return nil, fmt.Errorf("decode transcription metrics: %w", err)
			}
			item.Transcription = &transcription
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) GetSpeakingRecording(ctx context.Context, attemptID, partID uuid.UUID) (SpeakingRecording, error) {
	var item SpeakingRecording
	err := r.pool.QueryRow(ctx, `SELECT id, attempt_id, part_id, original_name, mime_type,
		storage_key, byte_size, revision, created_at, updated_at
		FROM speaking_recordings WHERE attempt_id=$1 AND part_id=$2`, attemptID, partID).Scan(
		&item.ID, &item.AttemptID, &item.PartID, &item.OriginalName,
		&item.MimeType, &item.StorageKey, &item.ByteSize, &item.Revision, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpeakingRecording{}, ErrRecordingNotFound
	}
	if err != nil {
		return SpeakingRecording{}, fmt.Errorf("get speaking recording: %w", err)
	}
	transcription, transcriptionErr := r.GetSpeakingTranscription(ctx, item.ID)
	if transcriptionErr != nil && !errors.Is(transcriptionErr, ErrNotFound) {
		return SpeakingRecording{}, transcriptionErr
	}
	if transcriptionErr == nil {
		item.Transcription = &transcription
	}
	return item, nil
}
