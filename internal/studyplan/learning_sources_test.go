package studyplan

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLearningWritingSpeakingBatchRetainsOldFeedbackAndTrustBoundaries(t *testing.T) {
	for _, section := range []string{"writing", "speaking"} {
		t.Run(section, func(t *testing.T) {
			h := setup(t)
			h.create(30)
			// The useful feedback is older than 100 other submitted attempts.
			m, v, q := seedPerformanceWS(h, section, 100)
			feedback := fmt.Sprintf(`{"tasks":[{"taskId":%q,"improvements":["Develop this argument"]}]}`, q.String())
			if section == "speaking" {
				feedback = fmt.Sprintf(`{"parts":[{"partId":%q,"transcript":"Original spoken response","improvements":["Develop this argument"]}]}`, q.String())
			}
			evaluate := func(a uuid.UUID) {
				if section == "writing" {
					h.sql(`INSERT INTO writing_evaluations(attempt_id,model,overall_band,task_response_band,coherence_band,lexical_resource_band,grammar_band,feedback) VALUES($1,'batch-fixture',7,7,7,7,7,$2)`, a, feedback)
				} else {
					h.sql(`INSERT INTO speaking_evaluations(attempt_id,model,overall_band,fluency_band,lexical_resource_band,grammar_band,pronunciation_band,feedback) VALUES($1,'batch-fixture',7,7,7,7,7,$2)`, a, feedback)
				}
			}
			makeAttempt := func(owner uuid.UUID, status string) uuid.UUID {
				a := performanceAttempt(h, owner, section, m, v, h.now.Add(-200*time.Hour), status, nil)
				if section == "writing" {
					h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer) VALUES($1,$2,'{"text":"Original written response"}')`, a, q)
				} else {
					// Speaking falls back to the persisted transcript when no typed
					// answer exists, and reports real audio provenance.
					h.sql(`INSERT INTO speaking_recordings(id,attempt_id,part_id,original_name,mime_type,storage_key,byte_size) VALUES($1,$2,$3,'fixture.webm','audio/webm',$4,100)`, uuid.New(), a, q, "batch-"+a.String())
				}
				return a
			}
			oldest := makeAttempt(h.user, "SUBMITTED")
			evaluate(oldest)
			foreign := makeAttempt(h.other, "SUBMITTED")
			evaluate(foreign)
			pending := makeAttempt(h.user, "PROCESSING")
			evaluate(pending)
			hidden := makeAttempt(h.user, "SUBMITTED")
			evaluate(hidden)
			mock, mockSession := uuid.New(), uuid.New()
			h.sql(`INSERT INTO full_mock_tests(id,slug,exam_type,title,listening_material_id,reading_material_id,writing_material_id,speaking_material_id) VALUES($1,$2,'academic','Hidden batch fixture',$3,$3,$3,$3)`, mock, "batch-"+mock.String(), m)
			h.sql(`INSERT INTO full_mock_sessions(id,mock_test_id,user_id,status) VALUES($1,$2,$3,'IN_PROGRESS')`, mockSession, mock, h.user)
			position := 3
			if section == "speaking" {
				position = 4
			}
			h.sql(`INSERT INTO full_mock_session_sections(session_id,position,skill,attempt_id) VALUES($1,$2,$3,$4)`, mockSession, position, section, hidden)
			late := makeAttempt(h.user, "SUBMITTED")
			l := learningGet(t, h)
			if len(l.Cycles) != 1 || l.Cycles[0].Source.AttemptID != oldest.String() {
				t.Fatalf("old history lost or foreign/pending/hidden/missing feedback exposed: %+v", l.Cycles)
			}
			c := l.Cycles[0]
			wantModality, wantAnswer := "typed", "Original written response"
			if section == "speaking" {
				wantModality, wantAnswer = "audio", "Original spoken response"
			}
			if c.Source.VersionID != v.String() || c.Source.OriginalAnswer != wantAnswer || c.Source.Modality != wantModality || c.Source.Prompt != "Fixture prompt " || c.Source.Provenance.Model == nil || *c.Source.Provenance.Model != "batch-fixture" || c.Source.FamilyKnown {
				t.Fatalf("batch source projection changed: %+v", c.Source)
			}
			evaluate(late)
			l = learningGet(t, h)
			if len(l.Cycles) != 2 {
				t.Fatalf("late feedback not discovered: %+v", l.Cycles)
			}
			h.sql(`UPDATE full_mock_sessions SET status='SUBMITTED' WHERE id=$1`, mockSession)
			l = learningGet(t, h)
			if len(l.Cycles) != 3 {
				t.Fatalf("submitted mock did not unlock source: %+v", l.Cycles)
			}
			for _, cycle := range l.Cycles {
				if cycle.Source.AttemptID == foreign.String() || cycle.Source.AttemptID == pending.String() {
					t.Fatal("foreign or pending feedback leaked")
				}
			}
		})
	}
}
