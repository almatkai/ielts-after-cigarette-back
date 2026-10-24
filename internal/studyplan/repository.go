package studyplan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type inputs struct {
	TaskPerformance []TaskPerformance `json:"-"` // Read-only projection; never a planner input.
	Origins         []contentOrigin
	Drills          []drillUnit
	Sources         []sourceMistake
	Goal            Goal
	Zone            string
	Materials       []material
	Results         []result
}

func loadInputs(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (inputs, error) {
	in := inputs{Zone: "UTC", Materials: []material{}, Results: []result{}}
	var examDate *time.Time
	if err := tx.QueryRow(ctx, `SELECT COALESCE(p.timezone,'UTC'),p.target_band::float8,p.exam_date,p.exam_type
 FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1`, userID).Scan(&in.Zone, &in.Goal.TargetBand, &examDate, &in.Goal.ExamType); err != nil {
		return in, err
	}
	if examDate != nil {
		in.Goal.ExamDate = ptr(examDate.Format(time.DateOnly))
	}
	examType := "academic"
	if in.Goal.ExamType != nil {
		examType = *in.Goal.ExamType
	}
	rows, err := tx.Query(ctx, `SELECT id,skill,title,duration,passage FROM (
 SELECT m.id,'reading' AS skill,v.title,COALESCE(v.duration_minutes,CASE WHEN m.material_kind='PASSAGE' THEN 20 ELSE 60 END)::int AS duration,m.material_kind='PASSAGE' AS passage
 FROM reading_materials m JOIN reading_material_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED' AND m.exam_type=$1 AND (
 EXISTS (SELECT 1 FROM reading_question_groups g JOIN reading_questions q ON q.group_id=g.id WHERE g.material_version_id=v.id)
 OR (m.material_kind='TEST' AND EXISTS (SELECT 1 FROM reading_test_passages tp JOIN reading_question_groups g ON g.material_version_id=tp.passage_material_version_id JOIN reading_questions q ON q.group_id=g.id WHERE tp.test_material_version_id=v.id)))
 UNION ALL
 SELECT m.id,'listening',v.title,v.duration_minutes::int,false FROM listening_tests m JOIN listening_test_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED' AND m.exam_type=$1 AND EXISTS (SELECT 1 FROM listening_parts p JOIN listening_question_groups g ON g.part_id=p.id JOIN listening_questions q ON q.group_id=g.id WHERE p.test_version_id=v.id)
 UNION ALL
 SELECT m.id,'writing',v.title,v.duration_minutes::int,false FROM writing_materials m JOIN writing_material_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED' AND v.exam_type=$1 AND jsonb_array_length(v.tasks)>0
 UNION ALL
 SELECT m.id,'speaking',v.title,CEIL((SELECT SUM(COALESCE((p->>'preparationSeconds')::numeric,0)+COALESCE((p->>'responseSeconds')::numeric,0)) FROM jsonb_array_elements(v.parts) p)/60)::int,false
 FROM speaking_materials m JOIN speaking_material_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED' AND v.exam_type=$1 AND jsonb_array_length(v.parts)>0
 ) catalog WHERE duration>0 ORDER BY skill,id`, examType)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var m material
		if err := rows.Scan(&m.ID, &m.Skill, &m.Title, &m.Duration, &m.Passage); err != nil {
			rows.Close()
			return in, err
		}
		in.Materials = append(in.Materials, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return in, err
	}
	rows, err = tx.Query(ctx, `SELECT a.id,COALESCE(
 (SELECT title FROM reading_material_versions v WHERE a.material_type='reading' AND v.id=a.material_version_id AND v.material_id=a.material_id),
 (SELECT title FROM listening_test_versions v WHERE a.material_type='listening' AND v.id=a.material_version_id AND v.test_id=a.material_id),
 (SELECT title FROM writing_material_versions v WHERE a.material_type='writing' AND v.id=a.material_version_id AND v.material_id=a.material_id),
 (SELECT title FROM speaking_material_versions v WHERE a.material_type='speaking' AND v.id=a.material_version_id AND v.material_id=a.material_id),''),a.material_type,a.material_id,a.material_version_id,a.status,a.started_at,a.submitted_at,a.score::int,a.max_score::int,
 CASE WHEN a.material_type IN ('writing','speaking') THEN COALESCE(w.overall_band,s.overall_band)::float8 ELSE a.band::float8 END,
 (SELECT COUNT(*)::int FROM attempt_answers ans WHERE ans.attempt_id=a.id AND ans.is_correct=false),
 COALESCE(w.feedback,s.feedback,'{}'::jsonb),
 EXISTS(SELECT 1 FROM attempts earlier WHERE earlier.user_id=a.user_id AND earlier.material_type=a.material_type AND earlier.material_id=a.material_id AND (earlier.started_at,earlier.id)<(a.started_at,a.id)),
 NOT EXISTS(SELECT 1 FROM full_mock_session_sections sec JOIN full_mock_sessions fm ON fm.id=sec.session_id WHERE sec.attempt_id=a.id AND fm.status<>'SUBMITTED'),
 EXISTS(SELECT 1 FROM study_plan_attempts claim WHERE claim.attempt_id=a.id),
 EXISTS(SELECT 1 FROM full_mock_session_sections sec WHERE sec.attempt_id=a.id),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',aa.question_id,'correct',aa.is_correct)) FROM attempt_answers aa WHERE aa.attempt_id=a.id AND aa.is_correct IS NOT NULL),'[]'::jsonb)
 FROM attempts a LEFT JOIN writing_evaluations w ON w.attempt_id=a.id LEFT JOIN speaking_evaluations s ON s.attempt_id=a.id
 WHERE a.user_id=$1 ORDER BY a.started_at DESC,a.id DESC`, userID)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var r result
		var questions []byte
		if err := rows.Scan(&r.ID, &r.Title, &r.Skill, &r.MaterialID, &r.VersionID, &r.Status, &r.StartedAt, &r.SubmittedAt, &r.Score, &r.MaxScore, &r.Band, &r.Wrong, &r.Feedback, &r.Repeat, &r.Reviewable, &r.Claimed, &r.FromMock, &questions); err != nil {
			return in, err
		}
		if err := json.Unmarshal(questions, &r.Questions); err != nil {
			rows.Close()
			return in, err
		}
		in.Results = append(in.Results, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return in, err
	}
	in.Origins, err = loadContentOrigins(ctx, tx, userID)
	if err != nil {
		return in, err
	}
	in.Sources, err = loadLearningSources(ctx, tx, userID, in.Results)
	if err != nil {
		return in, err
	}
	in.TaskPerformance, err = loadTaskPerformance(ctx, tx, userID)
	if err != nil {
		return in, err
	}
	in.Drills = builtinDrills()
	if examType != "academic" {
		in.Drills = []drillUnit{}
	}
	audio, e := loadShortListening(ctx, tx, examType)
	if e != nil {
		return in, e
	}
	in.Drills = append(in.Drills, audio...)
	meta, e := loadEditorialMetadata(ctx, tx)
	if e != nil {
		return in, e
	}
	in.Drills = applyEditorialMetadata(in.Drills, meta)
	applySourceEditorialMetadata(in.Sources, meta)
	for i := range in.Drills {
		u := &in.Drills[i]
		d := u.Drill
		if d.Modality == "script" {
			continue
		}
		in.Materials = append(in.Materials, material{ID: d.ID, Skill: d.Section, Title: d.Title, Duration: d.DurationMinutes, DrillID: d.ID, Unit: u})
	}
	return in, nil
}
func fingerprint(in inputs) string {
	raw, _ := json.Marshal(in)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
