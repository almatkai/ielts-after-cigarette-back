package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rowScanner is satisfied by both pgx.Row and pgx.Rows, so a single decoder can
// serve a single-row and a bulk query.
type rowScanner interface {
	Scan(...any) error
}

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
	GetStatus(context.Context, uuid.UUID, uuid.UUID) (StatusDetail, error)
	ListByUser(context.Context, uuid.UUID, string) ([]Summary, error)
	SaveAnswers(context.Context, uuid.UUID, []AnswerInput) error
	ListAnswers(context.Context, uuid.UUID) ([]Answer, error)
	// ListAnswersByAttempts loads the graded answers of many attempts in one
	// round trip, which keeps the mistakes page from paying a query per
	// historical attempt.
	ListAnswersByAttempts(context.Context, []uuid.UUID) (map[uuid.UUID][]Answer, error)
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
	rows, err := r.pool.Query(ctx, `WITH selected AS (
		SELECT * FROM attempts WHERE user_id=$1 AND ($2='' OR material_type=$2)
	) `+summarySelect+` ORDER BY a.started_at DESC`, userID, materialType)
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
	// Keep the same row lock as Submit, but acquire it and upsert in one
	// atomic statement. Ownership and exam expiration remain in the service.
	// Duplicate question IDs preserve the old last-answer-wins behavior.
	type payloadAnswer struct {
		QuestionID uuid.UUID      `json:"question_id"`
		Answer     map[string]any `json:"answer"`
	}
	payload := make([]payloadAnswer, 0, len(answers))
	indices := make(map[uuid.UUID]int, len(answers))
	for _, item := range answers {
		if index, ok := indices[item.QuestionID]; ok {
			payload[index].Answer = item.Answer
		} else {
			indices[item.QuestionID] = len(payload)
			payload = append(payload, payloadAnswer{item.QuestionID, item.Answer})
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal answers: %w", err)
	}
	var status string
	err = r.pool.QueryRow(ctx, `
		WITH locked AS MATERIALIZED (
			SELECT status FROM attempts WHERE id=$1 FOR UPDATE
		), saved AS (
			INSERT INTO attempt_answers (attempt_id, question_id, answer)
			SELECT $1, input.question_id, input.answer
			FROM jsonb_to_recordset($2::jsonb) AS input(question_id uuid, answer jsonb)
			CROSS JOIN locked WHERE locked.status='IN_PROGRESS'
			ON CONFLICT (attempt_id, question_id) DO UPDATE SET answer=EXCLUDED.answer
		)
		SELECT status FROM locked
	`, attemptID, encoded).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("save attempt answers: %w", err)
	}
	if status != StatusInProgress {
		return ErrAlreadySubmitted
	}
	return nil
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

func (r *PostgresRepository) ListAnswersByAttempts(ctx context.Context, attemptIDs []uuid.UUID) (map[uuid.UUID][]Answer, error) {
	items := map[uuid.UUID][]Answer{}
	if len(attemptIDs) == 0 {
		return items, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT attempt_id, question_id, answer, is_correct, points_awarded
		FROM attempt_answers WHERE attempt_id = ANY($1) ORDER BY attempt_id, question_id`, attemptIDs)
	if err != nil {
		return nil, fmt.Errorf("list attempt answers by attempts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var attemptID uuid.UUID
		var item Answer
		var raw []byte
		if err := rows.Scan(&attemptID, &item.QuestionID, &raw, &item.IsCorrect, &item.PointsAwarded); err != nil {
			return nil, fmt.Errorf("scan attempt answer: %w", err)
		}
		_ = json.Unmarshal(raw, &item.Answer)
		if item.Answer == nil {
			item.Answer = map[string]any{}
		}
		items[attemptID] = append(items[attemptID], item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attempt answers: %w", err)
	}
	return items, nil
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

	if len(result.Answers) > 0 {
		batch := &pgx.Batch{}
		for _, item := range result.Answers {
			answer, _ := json.Marshal(item.Answer)
			batch.Queue(`INSERT INTO attempt_answers
				(attempt_id, question_id, answer, is_correct, points_awarded)
				VALUES ($1,$2,$3::jsonb,$4,$5)
				ON CONFLICT (attempt_id, question_id) DO UPDATE SET
					answer = EXCLUDED.answer,
					is_correct = EXCLUDED.is_correct,
					points_awarded = EXCLUDED.points_awarded`,
				result.AttemptID, item.QuestionID, answer, item.IsCorrect, item.PointsAwarded)
		}
		br := tx.SendBatch(ctx, batch)
		for range result.Answers {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return fmt.Errorf("save graded answer batch: %w", err)
			}
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("close graded answer batch: %w", err)
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

const writingEvaluationSelect = `SELECT attempt_id, model, overall_band::double precision,
	task_response_band::double precision, coherence_band::double precision,
	lexical_resource_band::double precision, grammar_band::double precision,
	feedback, evaluated_at
	FROM writing_evaluations`

type writingEvaluationRow struct {
	attemptID                                         uuid.UUID
	model                                             string
	overallBand                                       float64
	taskResponse, coherence, lexicalResource, grammar float64
	feedback                                          []byte
	evaluatedAt                                       time.Time
}

func (row *writingEvaluationRow) scan(source rowScanner) error {
	return source.Scan(&row.attemptID, &row.model, &row.overallBand, &row.taskResponse,
		&row.coherence, &row.lexicalResource, &row.grammar, &row.feedback, &row.evaluatedAt)
}

func (row writingEvaluationRow) decode() (WritingEvaluation, error) {
	var stored struct {
		Summary  string                `json:"summary"`
		Criteria WritingCriteria       `json:"criteria"`
		Tasks    []WritingTaskFeedback `json:"tasks"`
	}
	if err := json.Unmarshal(row.feedback, &stored); err != nil {
		return WritingEvaluation{}, fmt.Errorf("decode writing evaluation: %w", err)
	}
	evaluation := WritingEvaluation{
		AttemptID:   row.attemptID,
		Model:       row.model,
		OverallBand: row.overallBand,
		Summary:     stored.Summary,
		Tasks:       stored.Tasks,
		EvaluatedAt: row.evaluatedAt,
	}
	evaluation.Criteria.TaskResponse = WritingCriterion{Band: row.taskResponse, Feedback: stored.Criteria.TaskResponse.Feedback}
	evaluation.Criteria.Coherence = WritingCriterion{Band: row.coherence, Feedback: stored.Criteria.Coherence.Feedback}
	evaluation.Criteria.LexicalResource = WritingCriterion{Band: row.lexicalResource, Feedback: stored.Criteria.LexicalResource.Feedback}
	evaluation.Criteria.Grammar = WritingCriterion{Band: row.grammar, Feedback: stored.Criteria.Grammar.Feedback}
	return evaluation, nil
}

func (r *PostgresRepository) GetWritingEvaluation(ctx context.Context, attemptID uuid.UUID) (WritingEvaluation, error) {
	var row writingEvaluationRow
	err := row.scan(r.pool.QueryRow(ctx, writingEvaluationSelect+` WHERE attempt_id=$1`, attemptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return WritingEvaluation{}, ErrNotFound
	}
	if err != nil {
		return WritingEvaluation{}, fmt.Errorf("get writing evaluation: %w", err)
	}
	return row.decode()
}

// GetWritingEvaluations loads the AI feedback of many attempts in one round
// trip, which the mistakes page needs for historical attempts.
func (r *PostgresRepository) GetWritingEvaluations(ctx context.Context, attemptIDs []uuid.UUID) (map[uuid.UUID]WritingEvaluation, error) {
	items := map[uuid.UUID]WritingEvaluation{}
	if len(attemptIDs) == 0 {
		return items, nil
	}
	rows, err := r.pool.Query(ctx, writingEvaluationSelect+` WHERE attempt_id = ANY($1)`, attemptIDs)
	if err != nil {
		return nil, fmt.Errorf("list writing evaluations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row writingEvaluationRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan writing evaluation: %w", err)
		}
		evaluation, err := row.decode()
		if err != nil {
			return nil, err
		}
		items[evaluation.AttemptID] = evaluation
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate writing evaluations: %w", err)
	}
	return items, nil
}

const speakingEvaluationSelect = `SELECT attempt_id, model, overall_band::double precision,
	fluency_band::double precision, lexical_resource_band::double precision,
	grammar_band::double precision, pronunciation_band::double precision,
	feedback, evaluated_at
	FROM speaking_evaluations`

type speakingEvaluationRow struct {
	attemptID                         uuid.UUID
	model                             string
	overallBand                       float64
	fluency, lexicalResource, grammar float64
	pronunciationBand                 *float64
	feedback                          []byte
	evaluatedAt                       time.Time
}

func (row *speakingEvaluationRow) scan(source rowScanner) error {
	return source.Scan(&row.attemptID, &row.model, &row.overallBand, &row.fluency,
		&row.lexicalResource, &row.grammar, &row.pronunciationBand, &row.feedback, &row.evaluatedAt)
}

func (row speakingEvaluationRow) decode() (SpeakingEvaluation, error) {
	var stored struct {
		Summary                string                 `json:"summary"`
		Criteria               SpeakingCriteria       `json:"criteria"`
		Parts                  []SpeakingPartFeedback `json:"parts"`
		PronunciationAvailable bool                   `json:"pronunciationAvailable"`
	}
	if err := json.Unmarshal(row.feedback, &stored); err != nil {
		return SpeakingEvaluation{}, fmt.Errorf("decode speaking evaluation: %w", err)
	}
	evaluation := SpeakingEvaluation{
		AttemptID:   row.attemptID,
		Model:       row.model,
		OverallBand: row.overallBand,
		Summary:     stored.Summary,
		Parts:       stored.Parts,
		EvaluatedAt: row.evaluatedAt,
	}
	evaluation.Criteria.Fluency = SpeakingCriterion{Band: row.fluency, Feedback: stored.Criteria.Fluency.Feedback}
	evaluation.Criteria.LexicalResource = SpeakingCriterion{Band: row.lexicalResource, Feedback: stored.Criteria.LexicalResource.Feedback}
	evaluation.Criteria.Grammar = SpeakingCriterion{Band: row.grammar, Feedback: stored.Criteria.Grammar.Feedback}
	evaluation.Criteria.Pronunciation = SpeakingCriterion{Band: 0, Feedback: stored.Criteria.Pronunciation.Feedback}
	evaluation.PronunciationAvailable = stored.PronunciationAvailable && row.pronunciationBand != nil
	if row.pronunciationBand != nil {
		evaluation.Criteria.Pronunciation.Band = *row.pronunciationBand
	} else if evaluation.Criteria.Pronunciation.Feedback == "" {
		evaluation.Criteria.Pronunciation.Feedback = "Наша система пока не может определить Pronunciation (произношение). Оценка сформирована по беглости, словарному запасу и грамматической точности."
	}
	return evaluation, nil
}

func (r *PostgresRepository) GetSpeakingEvaluation(ctx context.Context, attemptID uuid.UUID) (SpeakingEvaluation, error) {
	var row speakingEvaluationRow
	err := row.scan(r.pool.QueryRow(ctx, speakingEvaluationSelect+` WHERE attempt_id=$1`, attemptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SpeakingEvaluation{}, ErrNotFound
	}
	if err != nil {
		return SpeakingEvaluation{}, fmt.Errorf("get speaking evaluation: %w", err)
	}
	return row.decode()
}

// GetSpeakingEvaluations loads the AI feedback of many attempts in one round
// trip, which the mistakes page needs for historical attempts.
func (r *PostgresRepository) GetSpeakingEvaluations(ctx context.Context, attemptIDs []uuid.UUID) (map[uuid.UUID]SpeakingEvaluation, error) {
	items := map[uuid.UUID]SpeakingEvaluation{}
	if len(attemptIDs) == 0 {
		return items, nil
	}
	rows, err := r.pool.Query(ctx, speakingEvaluationSelect+` WHERE attempt_id = ANY($1)`, attemptIDs)
	if err != nil {
		return nil, fmt.Errorf("list speaking evaluations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row speakingEvaluationRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan speaking evaluation: %w", err)
		}
		evaluation, err := row.decode()
		if err != nil {
			return nil, err
		}
		items[evaluation.AttemptID] = evaluation
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate speaking evaluations: %w", err)
	}
	return items, nil
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
