package studyplan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type sourceMistake struct {
	Source   SourceEvidence
	SkillID  string
	Key      json.RawMessage
	Identity string
}

func loadLearningSources(ctx context.Context, tx pgx.Tx, user uuid.UUID, results []result) ([]sourceMistake, error) {
	out := []sourceMistake{}
	rows, err := tx.Query(ctx, `SELECT a.id,a.material_id,a.material_version_id,a.material_type,a.submitted_at,aa.question_id,aa.answer,q.question_type,q.prompt,q.answer,q.explanation,q.stimulus,q.content,q.audio FROM attempts a
 JOIN attempt_answers aa ON aa.attempt_id=a.id AND aa.is_correct=false
 JOIN LATERAL (
 SELECT g.question_type,r.prompt,r.answer,r.explanation,v.body AS stimulus,r.content,NULL::text AS audio FROM reading_questions r JOIN reading_question_groups g ON g.id=r.group_id JOIN reading_material_versions v ON v.id=g.material_version_id
 WHERE a.material_type='reading' AND r.id=aa.question_id AND (g.material_version_id=a.material_version_id OR EXISTS(SELECT 1 FROM reading_test_passages tp WHERE tp.test_material_version_id=a.material_version_id AND tp.passage_material_version_id=g.material_version_id))
 UNION ALL
 SELECT g.question_type,q.prompt,q.answer,q.explanation,g.context,q.content,CASE WHEN p.audio_asset_id IS NOT NULL THEN '/listening/media/'||p.audio_asset_id::text END FROM listening_questions q JOIN listening_question_groups g ON g.id=q.group_id JOIN listening_parts p ON p.id=g.part_id WHERE a.material_type='listening' AND q.id=aa.question_id AND p.test_version_id=a.material_version_id
 ) q ON true
 WHERE a.user_id=$1 AND a.status='SUBMITTED' AND NOT EXISTS(SELECT 1 FROM full_mock_session_sections sec JOIN full_mock_sessions fm ON fm.id=sec.session_id WHERE sec.attempt_id=a.id AND fm.status<>'SUBMITTED') ORDER BY a.submitted_at,aa.question_id`, user)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e SourceEvidence
		var when time.Time
		var answer, key, content []byte
		var question string
		if err = rows.Scan(&e.AttemptID, &e.MaterialID, &e.VersionID, &e.Modality, &when, &question, &answer, &e.QuestionType, &e.Prompt, &key, &e.Feedback, &e.Stimulus, &content, &e.AudioURL); err != nil {
			rows.Close()
			return nil, err
		}
		section := e.Modality
		e.Modality = "objective"
		e.QuestionID = ptr(question)
		rendered, actualKey := readableQuestion(content, key)
		key = actualKey
		e.Prompt = plainLearningText(e.Prompt)
		if rendered != "" {
			e.Prompt += "\n" + rendered
		}
		e.Stimulus = plainLearningText(e.Stimulus)
		e.OriginalAnswer = readableOriginalAnswer(answer, key)
		e.OccurredAt = when.UTC().Format(time.RFC3339)
		out = append(out, sourceMistake{e, questionSkill(section, e.QuestionType), key, e.AttemptID + ":" + question})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.Status != "SUBMITTED" || !r.Reviewable || (r.Skill != "writing" && r.Skill != "speaking") {
			continue
		}
		var version, model, prompt string
		var hasAudio bool
		var answersRaw []byte
		err = tx.QueryRow(ctx, `SELECT a.material_version_id::text,COALESCE(w.model,s.model,''),COALESCE((SELECT jsonb_object_agg(aa.question_id::text,aa.answer) FROM attempt_answers aa WHERE aa.attempt_id=a.id),'{}'),
 COALESCE((SELECT tasks::text FROM writing_material_versions v WHERE a.material_type='writing' AND v.id=a.material_version_id),(SELECT parts::text FROM speaking_material_versions v WHERE a.material_type='speaking' AND v.id=a.material_version_id),''),EXISTS(SELECT 1 FROM speaking_recordings sr WHERE sr.attempt_id=a.id)
 FROM attempts a LEFT JOIN writing_evaluations w ON w.attempt_id=a.id LEFT JOIN speaking_evaluations s ON s.attempt_id=a.id WHERE a.id=$1 AND a.user_id=$2`, r.ID, user).Scan(&version, &model, &answersRaw, &prompt, &hasAudio)
		if err != nil {
			return nil, err
		}
		var answers map[string]json.RawMessage
		_ = json.Unmarshal(answersRaw, &answers)
		var tasks []struct {
			ID           string `json:"id"`
			Prompt       string `json:"prompt"`
			Instructions string `json:"instructions"`
		}
		_ = json.Unmarshal([]byte(prompt), &tasks)
		prompts := map[string]string{}
		for _, t := range tasks {
			prompts[t.ID] = t.Prompt + " " + t.Instructions
		}
		var feedback struct {
			Tasks []struct {
				TaskID       string   `json:"taskId"`
				Improvements []string `json:"improvements"`
				Annotations  []struct {
					Quote       string `json:"quote"`
					Type        string `json:"type"`
					Severity    string `json:"severity"`
					Explanation string `json:"explanationRu"`
				} `json:"annotations"`
			} `json:"tasks"`
			Parts []struct {
				PartID       string   `json:"partId"`
				Transcript   string   `json:"transcript"`
				Improvements []string `json:"improvements"`
			} `json:"parts"`
		}
		if json.Unmarshal(r.Feedback, &feedback) != nil {
			continue
		}
		when := r.StartedAt
		if r.SubmittedAt != nil {
			when = *r.SubmittedAt
		}
		modality := "typed"
		if hasAudio {
			modality = "audio"
		}
		add := func(id, skill, typ, original, fragment, note, identity string) {
			if strings.TrimSpace(note) == "" {
				return
			}
			e := SourceEvidence{AttemptID: r.ID, QuestionID: ptr(id), MaterialID: r.MaterialID, VersionID: version, QuestionType: typ, Prompt: prompts[id], Stimulus: fragment, OriginalAnswer: original, Feedback: "Непроверенная рекомендация ИИ: " + note, OccurredAt: when.UTC().Format(time.RFC3339), Modality: modality}
			if model != "" {
				e.Provenance.Model = ptr(model)
			}
			out = append(out, sourceMistake{Source: e, SkillID: skill, Identity: r.ID + ":" + id + ":" + identity})
		}
		for _, t := range feedback.Tasks {
			original := answerText(answers[t.TaskID])
			if strings.TrimSpace(original) == "" || original == "null" {
				continue
			}
			for i, a := range t.Annotations {
				if a.Severity != "error" || a.Quote == "" || !strings.Contains(original, a.Quote) {
					continue
				}
				skill := map[string]string{"grammar": "ms-w-grammar", "vocabulary": "ms-w-lexis", "cohesion": "ms-w-coherence", "task_response": "ms-w-task-response"}[a.Type]
				if skill == "" {
					continue
				}
				add(t.TaskID, skill, a.Type, original, a.Quote, a.Explanation, fmt.Sprintf("annotation-%d", i))
			}
			if len(t.Improvements) > 0 {
				add(t.TaskID, "ms-w-task-response", "explicit-improvement", original, "", strings.Join(t.Improvements, "; "), "improvements")
			}
		}
		for _, p := range feedback.Parts {
			if len(p.Improvements) == 0 {
				continue
			}
			original := answerText(answers[p.PartID])
			if original == "" {
				original = p.Transcript
			}
			if strings.TrimSpace(original) == "" {
				continue
			}
			add(p.PartID, "ms-s-response-development", "explicit-improvement", original, "", strings.Join(p.Improvements, "; "), "improvements")
		}
	}

	return out, nil
}

func answerText(raw []byte) string {
	var v map[string]any
	if json.Unmarshal(raw, &v) == nil {
		for _, k := range []string{"value", "text", "transcript", "optionId"} {
			if s, ok := v[k].(string); ok {
				return s
			}
		}
	}
	return string(raw)
}

func loadShortListening(ctx context.Context, tx pgx.Tx, examType string) ([]drillUnit, error) {
	rows, err := tx.Query(ctx, `SELECT t.id,v.id,p.id,q.id,v.title,g.question_type,g.instructions,g.context,q.prompt,q.content,q.answer,q.explanation,p.audio_asset_id
 FROM listening_tests t JOIN listening_test_versions v ON v.id=t.published_version_id JOIN listening_parts p ON p.test_version_id=v.id
 JOIN listening_media media ON media.id=p.audio_asset_id AND media.kind='audio' AND media.byte_size>0
 JOIN listening_question_groups g ON g.part_id=p.id JOIN listening_questions q ON q.group_id=g.id
 WHERE t.status='PUBLISHED' AND t.exam_type=$1 ORDER BY t.id,p.position,g.position,q.position`, examType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []drillUnit{}
	for rows.Next() {
		var material, version, part, q, title, typ, instructions, stimulus, prompt, audio, teacher string
		var key, content []byte
		if err = rows.Scan(&material, &version, &part, &q, &title, &typ, &instructions, &stimulus, &prompt, &content, &key, &teacher, &audio); err != nil {
			return nil, err
		}
		rendered, key := readableQuestion(content, key)
		d := Drill{ID: "listening-" + version + "-" + q, SkillID: questionSkill("listening", typ), Section: "listening", Title: title + " · focused question", DurationMinutes: 12, Modality: "audio", Version: version, Introduction: instructions + " Listen to the published part audio and answer the focused question. Allow up to 12 minutes; stop and return later if needed. This is not a timed full-section score.", Stimulus: plainLearningText(stimulus), Task: plainLearningText(prompt) + "\n" + rendered, RightsProvenance: "Published catalog asset; source rights and family metadata not independently audited.", EditorialStatus: "draft", AudioURL: ptr("/listening/media/" + audio), SourceMaterialID: ptr(material), SourceVersionID: ptr(version), SourceQuestionID: ptr(q)}
		out = append(out, drillUnit{d, key, teacher})
	}
	return out, rows.Err()
}
