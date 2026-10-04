package attempts

import (
	"context"

	"github.com/google/uuid"
)

// MistakeAttempt is intentionally small: opening the bank must not load
// grading structures, passages, transcripts or AI feedback.
type MistakeAttempt struct {
	Summary
	MistakeCount *int `json:"mistakeCount"`
}

type MistakeAttemptsPage struct {
	Items   []MistakeAttempt `json:"items"`
	Page    int              `json:"page"`
	HasNext bool             `json:"hasNext"`
}

func (s *Service) MistakeAttempts(ctx context.Context, userID uuid.UUID, materialType string, page, limit int) (MistakeAttemptsPage, error) {
	repository, ok := s.repository.(interface {
		ListMistakeAttempts(context.Context, uuid.UUID, string, int, int) (MistakeAttemptsPage, error)
	})
	if !ok {
		return MistakeAttemptsPage{}, ErrUnsupportedMaterial
	}
	return repository.ListMistakeAttempts(ctx, userID, materialType, page, limit)
}

// LIMIT precedes all question/answer work. Only metadata for the selected page
// is read; the extra row tells the client whether another page exists.
const mistakeAttemptsSQL = `
	WITH page AS MATERIALIZED (
		SELECT a.* FROM attempts a
		WHERE a.user_id = $1 AND a.material_type = $2 AND a.status = 'SUBMITTED'
		AND (
			(a.material_type IN ('reading', 'listening') AND a.score < a.max_score)
			OR (a.material_type = 'writing' AND EXISTS (SELECT 1 FROM writing_evaluations e WHERE e.attempt_id = a.id))
			OR (a.material_type = 'speaking' AND EXISTS (SELECT 1 FROM speaking_evaluations e WHERE e.attempt_id = a.id))
		)
		ORDER BY a.submitted_at DESC, a.id DESC
		LIMIT $3 OFFSET $4
	)
	SELECT a.id, a.user_id, a.material_type, a.material_id, a.material_version_id,
		a.status, a.score, a.max_score, a.band::double precision, a.started_at, a.submitted_at,
		COALESCE(lv.title, rv.title, wv.title, sv.title, ''),
		COALESCE(lt.slug, rm.slug, wm.slug, sm.slug, ''),
		CASE WHEN a.material_type IN ('listening', 'reading') THEN (
			SELECT COUNT(*)::int FROM (
				SELECT q.id FROM listening_parts p
				JOIN listening_question_groups g ON g.part_id = p.id
				JOIN listening_questions q ON q.group_id = g.id
				WHERE a.material_type = 'listening' AND p.test_version_id = a.material_version_id
				UNION ALL
				SELECT q.id FROM reading_question_groups g
				JOIN reading_questions q ON q.group_id = g.id
				JOIN (
					SELECT a.material_version_id AS version_id
					UNION
					SELECT passage_material_version_id FROM reading_test_passages
					WHERE test_material_version_id = a.material_version_id
				) versions ON versions.version_id = g.material_version_id
				WHERE a.material_type = 'reading'
			) questions
			LEFT JOIN attempt_answers answer ON answer.attempt_id = a.id AND answer.question_id = questions.id
			WHERE answer.is_correct IS DISTINCT FROM TRUE
		) ELSE NULL END
	FROM page a
	LEFT JOIN listening_tests lt ON lt.id = a.material_id AND a.material_type = 'listening'
	LEFT JOIN listening_test_versions lv ON lv.id = a.material_version_id AND a.material_type = 'listening'
	LEFT JOIN reading_materials rm ON rm.id = a.material_id AND a.material_type = 'reading'
	LEFT JOIN reading_material_versions rv ON rv.id = a.material_version_id AND a.material_type = 'reading'
	LEFT JOIN writing_materials wm ON wm.id = a.material_id AND a.material_type = 'writing'
	LEFT JOIN writing_material_versions wv ON wv.id = a.material_version_id AND a.material_type = 'writing'
	LEFT JOIN speaking_materials sm ON sm.id = a.material_id AND a.material_type = 'speaking'
	LEFT JOIN speaking_material_versions sv ON sv.id = a.material_version_id AND a.material_type = 'speaking'
	ORDER BY a.submitted_at DESC, a.id DESC
`

func (r *PostgresRepository) ListMistakeAttempts(ctx context.Context, userID uuid.UUID, materialType string, page, limit int) (MistakeAttemptsPage, error) {
	result := MistakeAttemptsPage{Items: []MistakeAttempt{}, Page: page}
	rows, err := r.pool.Query(ctx, mistakeAttemptsSQL, userID, materialType, limit+1, (page-1)*limit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item MistakeAttempt
		if err := rows.Scan(&item.ID, &item.UserID, &item.MaterialType, &item.MaterialID, &item.MaterialVersionID,
			&item.Status, &item.Score, &item.MaxScore, &item.Band, &item.StartedAt, &item.SubmittedAt,
			&item.TestTitle, &item.TestSlug, &item.MistakeCount); err != nil {
			return result, err
		}
		if len(result.Items) == limit {
			result.HasNext = true
			continue
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

// Review contexts are shared by all questions from the same passage/part.
// Keep the attempt-detail contract unchanged for other consumers.
type MistakeContext struct {
	PassageBody string `json:"passageBody,omitempty"`
	Transcript  string `json:"transcript,omitempty"`
}

type BankReviewAnswer struct {
	ReviewAnswer
	ContextIndex int `json:"contextIndex"`
}

type MistakeDetail struct {
	Attempt            Attempt             `json:"attempt"`
	Review             []BankReviewAnswer  `json:"review"`
	Contexts           []MistakeContext    `json:"contexts"`
	WritingEvaluation  *WritingEvaluation  `json:"writingEvaluation,omitempty"`
	SpeakingEvaluation *SpeakingEvaluation `json:"speakingEvaluation,omitempty"`
}

func (s *Service) MistakeDetail(ctx context.Context, userID, attemptID uuid.UUID) (MistakeDetail, error) {
	detail, err := s.Get(ctx, userID, attemptID)
	if err != nil {
		return MistakeDetail{}, err
	}
	return bankDetail(detail), nil
}

func bankDetail(detail Detail) MistakeDetail {
	result := MistakeDetail{
		Attempt: detail.Attempt,
		Review:  []BankReviewAnswer{}, Contexts: []MistakeContext{},
		WritingEvaluation: detail.WritingEvaluation, SpeakingEvaluation: detail.SpeakingEvaluation,
	}
	indices := map[MistakeContext]int{}
	for _, item := range detail.Review {
		if item.IsCorrect {
			continue
		}
		shared := MistakeContext{PassageBody: item.PassageBody, Transcript: item.Transcript}
		index, ok := indices[shared]
		if !ok {
			index = len(result.Contexts)
			indices[shared] = index
			result.Contexts = append(result.Contexts, shared)
		}
		item.PassageBody, item.Transcript = "", ""
		result.Review = append(result.Review, BankReviewAnswer{ReviewAnswer: item, ContextIndex: index})
	}
	return result
}
