package studyplan

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/google/uuid"
)

func learningCall[T any](t *testing.T, h *harness, method, path string, input any, status int) T {
	t.Helper()
	w := h.request(method, "/study-plan/learning"+path, input, h.token)
	if w.Code != status {
		t.Fatalf("%s %s = %d want %d: %s", method, path, w.Code, status, w.Body)
	}
	var out T
	if status == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func learningGet(t *testing.T, h *harness) Learning {
	return learningCall[Learning](t, h, "GET", "", nil, 200)
}
func sourceCycle(t *testing.T, h *harness) Cycle {
	t.Helper()
	id, _, q := h.seedReading(20)
	a, _, _, err := h.attempts.Start(t.Context(), h.user, "reading", id)
	if err != nil {
		t.Fatal(err)
	}
	// Real attempt submit grades persisted question keys; no fabricated score input.
	_, err = h.attempts.Submit(t.Context(), h.user, a.ID, answerInput(q, "FALSE"))
	if err != nil {
		t.Fatal(err)
	}
	out := learningGet(t, h)
	for _, c := range out.Cycles {
		if c.Source.AttemptID == a.ID.String() {
			return c
		}
	}
	t.Fatal("real wrong attempt did not create a cycle")
	return Cycle{}
}
func answerInput(q uuid.UUID, value string) attempts.SaveAnswersInput {
	return attempts.SaveAnswersInput{Answers: []attempts.AnswerInput{{QuestionID: q, Answer: map[string]any{"value": value}}}}
}

func correctionInput(c Cycle, key, text string) map[string]any {
	return map[string]any{"expectedRevision": c.Revision, "idempotencyKey": key, "text": text, "explanation": "Source evidence supports this answer; do not infer missing facts.", "assisted": false}
}
func TestLearningHTTPRealAttemptCorrectionReloadOwnershipAndRace(t *testing.T) {
	h := setup(t)
	h.create(30)
	c := sourceCycle(t, h)
	if c.SkillID != "ms-r-tfng" || c.Source.OriginalAnswer != "FALSE" || c.Source.FamilyKnown {
		t.Fatalf("bad concrete source %+v", c)
	}
	before := learningGet(t, h)
	again := learningGet(t, h)
	if len(again.Cycles) != len(before.Cycles) || again.Revision != before.Revision {
		t.Fatal("GET duplicated cycles/revisions")
	}
	foreign := h.request("GET", "/study-plan/learning/cycles/"+c.ID, nil, h.otherToken)
	if foreign.Code != 404 {
		t.Fatalf("owner leak %d", foreign.Code)
	}
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/view", map[string]any{"expectedRevision": c.Revision}, 200)
	if c.Correction != nil || c.Status != "needs-practice" {
		t.Fatal("view fabricated correction")
	}
	wrong := correctionInput(c, "wrong", "not empty but incorrect")
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", wrong, 200)
	if c.Correction.Verdict != "incorrect" || c.Status != "needs-practice" {
		t.Fatalf("nonempty correction accepted %+v", c)
	}
	retry := learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", wrong, 200)
	if retry.Revision != c.Revision {
		t.Fatal("idempotency failed")
	}
	wrong["text"] = "TRUE"
	learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", wrong, 409)
	corrected := correctionInput(c, "correct", " true ")
	var wg sync.WaitGroup
	out := make(chan *httptest.ResponseRecorder, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out <- h.request("POST", "/study-plan/learning/cycles/"+c.ID+"/correction", corrected, h.token)
		}()
	}
	wg.Wait()
	close(out)
	for w := range out {
		if w.Code != 200 {
			t.Fatalf("same-key race %d %s", w.Code, w.Body)
		}
	}
	c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	if c.Status != "awaiting-transfer" || c.Correction.Verdict != "correct" || c.RemainingChecks != 2 {
		t.Fatalf("correction result %+v", c)
	}
	var original string
	if err := h.pool.QueryRow(t.Context(), `SELECT answer->>'value' FROM attempt_answers WHERE attempt_id=$1 AND question_id=$2`, c.Source.AttemptID, *c.Source.QuestionID).Scan(&original); err != nil || original != "FALSE" {
		t.Fatalf("original answer overwritten %q %v", original, err)
	}
	learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", corrected, 200)
	stale := correctionInput(c, "stale", "TRUE")
	stale["expectedRevision"] = 1
	learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", stale, 409)
}
func approveFixture(t *testing.T, h *harness, u drillUnit, family string) {
	t.Helper()
	h.sql(`INSERT INTO study_learning_content_metadata(kind,reference_id,version,section,family_id,status,content_hash,revision,review_notes,reviewer_id,reviewed_at) VALUES('drill',$1,$2,$3,$4,'reviewed',$5,1,'Isolated test editorial fixture only',$6,CURRENT_TIMESTAMP) ON CONFLICT(kind,reference_id,version) DO UPDATE SET status='reviewed',family_id=EXCLUDED.family_id,content_hash=EXCLUDED.content_hash`, u.Drill.ID, u.Drill.Version, u.Drill.Section, family, drillContentHash(u), h.user)
}
func startDrill(t *testing.T, h *harness, id, key string, cycle *Cycle) Session {
	t.Helper()
	l := learningGet(t, h)
	input := map[string]any{"expectedRevision": l.Revision, "idempotencyKey": key}
	if cycle != nil {
		input["cycleId"] = cycle.ID
	}
	return learningCall[Session](t, h, "POST", "/drills/"+id+"/start", input, 200)
}
func submitDrill(t *testing.T, h *harness, s Session, key, text string, assisted bool) Session {
	return learningCall[Session](t, h, "POST", "/sessions/"+s.ID+"/submit", map[string]any{"expectedRevision": s.Revision, "idempotencyKey": key, "text": text, "assisted": assisted}, 200)
}
func TestLearningDraftPracticeAndActualWritingNoFakeMastery(t *testing.T) {
	h := setup(t)
	h.create(30)
	l := learningGet(t, h)
	foundWriting, foundScript := false, false
	for _, d := range l.Drills {
		if d.MasteryEligible {
			t.Fatal("draft promoted")
		}
		if d.Section == "writing" && !foundWriting {
			foundWriting = true
			if d.Modality != "typed" || d.DurationMinutes > 30 || !strings.Contains(d.Task, "Write") {
				t.Fatalf("fake writing drill %+v", d)
			}
			s := startDrill(t, h, d.ID, "w-start", nil)
			s = submitDrill(t, h, s, "w-submit", "Overall, the largest increase occurred in wind power, while hydro remained broadly stable.", false)
			if *s.Verdict != "unverified" || s.Independent {
				t.Fatal("Writing auto mastery")
			}
		}
		if d.Modality == "script" {
			foundScript = true
			if d.Section != "listening" {
				t.Fatal("script incorrectly typed")
			}
		}
	}
	if !foundWriting || !foundScript {
		t.Fatal("draft bank not available")
	}
	s := startDrill(t, h, "iac-r-library", "r-start", nil)
	s = submitDrill(t, h, s, "r-wrong", "TRUE", false)
	l = learningGet(t, h)
	var c *Cycle
	for i := range l.Cycles {
		if l.Cycles[i].Source.SessionID != nil && *l.Cycles[i].Source.SessionID == s.ID {
			c = &l.Cycles[i]
		}
	}
	if c == nil || c.Source.AttemptID != "" || c.Source.FamilyKnown || c.Source.DrillID == nil {
		t.Fatalf("draft source provenance %+v", c)
	}
	c2 := learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(*c, "r-correct", "FALSE"), 200)
	next := startDrill(t, h, "iac-r-transport", "draft-transfer", &c2)
	next = submitDrill(t, h, next, "draft-submit", "NOT GIVEN", false)
	if next.Independent {
		t.Fatal("draft transfer qualified")
	}
	c2 = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	if c2.Status == "demonstrated" || c2.RemainingChecks != 2 {
		t.Fatal("draft mastery")
	}
}
func TestLearningIndependentDelayedTransferWithdrawalAndCorrectionGeneration(t *testing.T) {
	h := setup(t)
	h.create(30)
	units := builtinDrills()
	for _, id := range []string{"iac-r-library", "iac-r-transport", "iac-r-volunteer"} {
		u := unitByID(units, id)
		approveFixture(t, h, *u, *u.Drill.FamilyID)
	}
	s := startDrill(t, h, "iac-r-library", "source", nil)
	s = submitDrill(t, h, s, "source-submit", "TRUE", false)
	l := learningGet(t, h)
	var c Cycle
	for _, x := range l.Cycles {
		if x.Source.SessionID != nil && *x.Source.SessionID == s.ID {
			c = x
		}
	}
	if !c.Source.FamilyKnown {
		t.Fatal("reviewed family unavailable")
	}
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "fix", "FALSE"), 200)
	one := startDrill(t, h, "iac-r-transport", "check1", &c)
	// Viewing changes public cycle revision but not the correction generation.
	c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/view", map[string]any{"expectedRevision": c.Revision}, 200)
	one = submitDrill(t, h, one, "check1-submit", "NOT GIVEN", false)
	if !one.Independent {
		t.Fatalf("new reviewed family failed %s", one.IndependenceReason)
	}
	c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	if c.RemainingChecks != 1 {
		t.Fatalf("first check missing %+v", c)
	}
	h.now = h.now.AddDate(0, 0, 3)
	two := startDrill(t, h, "iac-r-volunteer", "check2", &c)
	two = submitDrill(t, h, two, "check2-submit", "TRUE", false)
	if !two.Independent {
		t.Fatal("second not independent")
	}
	c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	if c.Status != "demonstrated" || c.RemainingChecks != 0 {
		t.Fatalf("demonstration unreachable %+v", c)
	}
	// Retrospective status must honor withdrawal of either check's content.
	h.sql(`UPDATE study_learning_content_metadata SET status='withdrawn',revision=revision+1 WHERE reference_id='iac-r-volunteer'`)
	c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	if c.Status == "demonstrated" || c.RemainingChecks != 1 {
		t.Fatal("withdrawn check retained mastery")
	}
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "edit", "FALSE"), 200)
	if len(c.Checks) != 0 || c.RemainingChecks != 2 {
		t.Fatal("correction edit retained stale mastery")
	}
}
func TestLearningDeadlineBudgetCoverageAndSavedSessions(t *testing.T) {
	for _, budget := range []int{30, 45, 60, 90} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			h := setup(t)
			h.seedReading(20)
			h.sql(`UPDATE user_profiles SET exam_date=$2 WHERE user_id=$1`, h.user, h.now.AddDate(0, 0, 10))
			r := h.create(budget)
			counts := map[string]int{}
			for _, d := range r.Plan.Days {
				if d.Date >= *r.Goal.ExamDate {
					if !d.IsRest || d.ReadOnlyReason == nil || len(d.Assignments) != 0 {
						t.Fatalf("future after exam %+v", d)
					}
				} else {
					if d.PlannedMinutes > d.BudgetMinutes {
						t.Fatal("budget exceeded")
					}
					for _, a := range d.Assignments {
						if a.DrillID != nil || a.MaterialID != nil {
							counts[a.Skill]++
						}
					}
				}
			}
			for _, skill := range []string{"reading", "writing", "speaking"} {
				if counts[skill] < 2 {
					t.Fatalf("section starved %d %+v", budget, counts)
				}
			}
			l := learningGet(t, h)
			for _, sec := range l.Inventory.Sections {
				if sec.Section == "listening" && (sec.Available || len(sec.Limitations) == 0) {
					t.Fatal("missing listening silently omitted")
				}
			}
			s := startDrill(t, h, "iac-r-library", "saved", nil)
			again := startDrill(t, h, "iac-r-library", "new-key", nil)
			if s.ID != again.ID {
				t.Fatal("reload duplicate session")
			}
			h.now = h.now.AddDate(0, 0, 10)
			l = learningGet(t, h)
			if !l.ReadOnly {
				t.Fatal("exam read-only absent")
			}
			learningCall[Session](t, h, "POST", "/sessions/"+s.ID+"/submit", map[string]any{"expectedRevision": s.Revision, "idempotencyKey": "late", "text": "FALSE", "assisted": false}, 409)
			saved := learningCall[Session](t, h, "GET", "/sessions/"+s.ID, nil, 200)
			if saved.Status != "in-progress" {
				t.Fatal("saved session changed")
			}
		})
	}
}
func TestLearningLocalDelayedBoundary(t *testing.T) {
	for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Honolulu"} {
		t.Run(zone, func(t *testing.T) {
			st := state{Learning: learningState{Zone: zone}}
			c := Correction{SavedAt: "2026-10-24T11:30:00Z"}
			z, _ := time.LoadLocation(zone)
			expected := localDate(time.Date(2026, 10, 24, 11, 30, 0, 0, time.UTC), z)
			if correctionDay(&st, &c) != expected {
				t.Fatalf("UTC/local anchor %s %s", correctionDay(&st, &c), expected)
			}
		})
	}
}

func TestLearningQuestionKeysAliasesSetsAndReadableStimulus(t *testing.T) {
	content := json.RawMessage(`{"options":[{"id":"opaque-17","text":"<p>North campus</p>"},{"id":"opaque-82","text":"South campus"},{"id":"opaque-93","text":"West campus"}],"answer":"never expose","hint":"hidden hint"}`)
	task, key := readableQuestion(content, json.RawMessage(`{"optionIds":["opaque-17","opaque-93"]}`))
	if strings.Contains(task, "opaque") || strings.Contains(task, "never expose") || strings.Contains(task, "hidden hint") || !strings.Contains(task, "A) North campus") {
		t.Fatalf("unreadable/private task %s", task)
	}
	for _, tc := range []struct {
		key     json.RawMessage
		answer  string
		correct bool
	}{
		{key, "A, C", true}, {key, "C, A", true}, {key, "A, A", false}, {key, "A", false}, {key, "A, B, C", false},
		{json.RawMessage(`{"value":"centre","accepted":["center"]}`), "center", true},
		{json.RawMessage(`{"value":"NOT_GIVEN"}`), "not given", true},
		{json.RawMessage(`{}`), "anything", false}, {json.RawMessage(`{"optionIds":[]}`), `{"optionIds":[]}`, false},
		{json.RawMessage(`{"accepted":[]}`), "", false},
	} {
		if got := objectiveCorrect(tc.key, tc.answer); got != tc.correct {
			t.Fatalf("key=%s answer=%s got=%v", tc.key, tc.answer, got)
		}
	}
	plain := plainLearningText("<p>First &amp; second.</p><p>Third.</p><script>secret()</script>")
	if strings.Contains(plain, "<") || strings.Contains(plain, "secret") || !strings.Contains(plain, "First & second.\nThird.") {
		t.Fatalf("HTML leaked %s", plain)
	}
	for _, u := range builtinDrills() {
		if u.Drill.Section == "writing" || u.Drill.Section == "speaking" {
			for _, internal := range []string{"tableData", "taskCard", "svgSpec", "examinerQuestions"} {
				if strings.Contains(u.Drill.Stimulus, internal) {
					t.Fatalf("internal schema in task %s", u.Drill.ID)
				}
			}
		}
	}
}
func TestLearningMaintenanceMatrixAndRecentQuestionSupersession(t *testing.T) {
	for _, budget := range []int{30, 45, 60, 90} {
		for _, wrongSection := range []string{"reading", "listening"} {
			t.Run(fmt.Sprintf("%d-%s", budget, wrongSection), func(t *testing.T) {
				settings := allDays(budget)
				p := makePlan("2026-10-26", settings, 165)
				bank := []material{}
				for _, section := range skills {
					for i := 0; i < 40; i++ {
						bank = append(bank, material{ID: fmt.Sprintf("%s-%d", section, i), Skill: section, Duration: 10})
					}
				}
				latest := date("2026-10-25")
				older := date("2026-10-24")
				results := []result{}
				for _, section := range skills {
					results = append(results, result{ID: "latest-" + section, Skill: section, Status: "SUBMITTED", SubmittedAt: &latest, Questions: []questionEvidence{{section + "-q", true}}})
				}
				results = append(results, result{ID: "old", Skill: wrongSection, Status: "SUBMITTED", SubmittedAt: &older, Wrong: 100, Questions: []questionEvidence{{wrongSection + "-q", false}}})
				fillFuture(&p, settings, p.StartDate, p.StartDate, bank, results, false)
				count := map[string]int{}
				for _, d := range p.Days[:14] {
					if d.PlannedMinutes > budget {
						t.Fatal("budget exceeded")
					}
					for _, a := range d.Assignments {
						if a.MaterialID != nil {
							count[a.Skill]++
						}
						if strings.Contains(a.Reason, "неверных ответов — 100") {
							t.Fatal("lifetime wrong priority retained")
						}
					}
				}
				for _, section := range skills {
					if count[section] < 2 {
						t.Fatalf("coverage floor starved %s %+v", section, count)
					}
				}
			})
		}
	}
}
func TestLearningPostMockReviewNextAvailableDayWithinBudget(t *testing.T) {
	settings := allDays(30)
	p := makePlan("2026-10-26", settings, 165)
	submitted := date("2026-10-25")
	r := result{ID: "mock-attempt", Skill: "reading", Status: "SUBMITTED", Wrong: 1, SubmittedAt: &submitted, Reviewable: true, FromMock: true}
	fillFuture(&p, settings, p.StartDate, p.StartDate, []material{{ID: "s", Skill: "speaking", Duration: 10}}, []result{r}, false)
	found := false
	for _, a := range p.Days[0].Assignments {
		found = found || a.Kind == "REVIEW"
	}
	if !found || p.Days[0].PlannedMinutes > 30 {
		t.Fatalf("mock review delayed/overbudget %+v", p.Days[0])
	}
}
func TestLearningWritingLateFeedbackAndUnverifiedRevision(t *testing.T) {
	h := setup(t)
	h.create(30)
	material, version, task, attempt := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	h.sql(`INSERT INTO writing_materials(id,slug) VALUES($1,$2)`, material, "writing-"+material.String())
	tasks, _ := json.Marshal([]map[string]any{{"id": task, "type": "task2", "prompt": "Discuss the role of public libraries."}})
	h.sql(`INSERT INTO writing_material_versions(id,material_id,version_number,exam_type,difficulty,title,tasks) VALUES($1,$2,1,'academic','intermediate','Writing real feedback fixture',$3)`, version, material, tasks)
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,started_at) VALUES($1,$2,'writing',$3,$4,'PROCESSING',$5)`, attempt, h.user, material, version, h.now)
	h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer) VALUES($1,$2,'{"text":"Libraries provides shared resources."}')`, attempt, task)
	l := learningGet(t, h)
	for _, c := range l.Cycles {
		if c.Source.AttemptID == attempt.String() {
			t.Fatal("pending feedback created weakness")
		}
	}
	feedback := fmt.Sprintf(`{"criteria":{"grammar":{"feedback":"Clear grammatical control"}},"tasks":[{"taskId":%q,"improvements":[],"annotations":[]}]}`, task.String())
	h.sql(`UPDATE attempts SET status='SUBMITTED',score=0,max_score=0,submitted_at=$2 WHERE id=$1`, attempt, h.now)
	h.sql(`INSERT INTO writing_evaluations(attempt_id,model,overall_band,task_response_band,coherence_band,lexical_resource_band,grammar_band,feedback) VALUES($1,'fixture-model',7,7,7,7,7,$2)`, attempt, feedback)
	l = learningGet(t, h)
	for _, c := range l.Cycles {
		if c.Source.AttemptID == attempt.String() {
			t.Fatal("positive criterion feedback created weakness")
		}
	}
	feedback = fmt.Sprintf(`{"tasks":[{"taskId":%q,"annotations":[{"quote":"Libraries provides","type":"grammar","severity":"error","explanationRu":"Проверьте согласование подлежащего и сказуемого."}]}]}`, task.String())
	h.sql(`UPDATE writing_evaluations SET feedback=$2 WHERE attempt_id=$1`, attempt, feedback)
	l = learningGet(t, h)
	var c Cycle
	for _, x := range l.Cycles {
		if x.Source.AttemptID == attempt.String() {
			c = x
		}
	}
	if c.ID == "" || c.SkillID != "ms-w-grammar" || c.Source.Stimulus != "Libraries provides" || c.Source.Provenance.Model == nil {
		t.Fatalf("wrong grammar diagnosis %+v", c)
	}
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "writing-revision", "Libraries provide shared resources."), 200)
	if c.Status != "correction-saved" || c.Correction.Verdict != "unverified" {
		t.Fatal("W revision auto succeeded")
	}
}

func TestLearningPriorOriginsCannotBeRelabelledAsNewFamilies(t *testing.T) {
	h := setup(t)
	h.create(30)
	material, version, part, group, q1, q2, audio, attempt := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	h.sql(`INSERT INTO listening_tests(id,slug,exam_type,created_by,updated_by) VALUES($1,$2,'academic',$3,$3)`, material, "listen-"+material.String(), h.user)
	h.sql(`INSERT INTO listening_media(id,kind,original_name,mime_type,storage_key,byte_size,created_by) VALUES($1,'audio','test.mp3','audio/mpeg',$2,100,$3)`, audio, "isolated-test-"+audio.String(), h.user)
	h.sql(`INSERT INTO listening_test_versions(id,test_id,version_number,title,duration_minutes,created_by) VALUES($1,$2,1,'Original audio fixture',40,$3)`, version, material, h.user)
	h.sql(`INSERT INTO listening_parts(id,test_version_id,position,audio_asset_id) VALUES($1,$2,1,$3)`, part, version, audio)
	h.sql(`INSERT INTO listening_question_groups(id,part_id,position,question_type,context) VALUES($1,$2,1,'short_answer','A published audio part with two focused questions.')`, group, part)
	for i, q := range []uuid.UUID{q1, q2} {
		h.sql(`INSERT INTO listening_questions(id,group_id,position,number,prompt,answer,explanation) VALUES($1,$2,$3,$3,'Which destination?', '{"accepted":["north"]}','The final spoken destination is north.')`, q, group, i+1)
	}
	h.sql(`UPDATE listening_tests SET status='PUBLISHED',published_version_id=$2,current_version_id=$2 WHERE id=$1`, material, version)
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,started_at,submitted_at) VALUES($1,$2,'listening',$3,$4,'SUBMITTED',0,2,$5,$5)`, attempt, h.user, material, version, h.now.Add(-time.Hour))
	for _, q := range []uuid.UUID{q1, q2} {
		h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer,is_correct,points_awarded) VALUES($1,$2,'{"value":"south"}',false,0)`, attempt, q)
	}
	h.sql(`INSERT INTO study_learning_content_metadata(kind,reference_id,version,section,family_id,status,content_hash,revision,review_notes,reviewer_id,reviewed_at) VALUES('material',$1,$2,'listening','source-family','reviewed',$3,1,'Isolated origin fixture',$4,CURRENT_TIMESTAMP)`, material.String(), version.String(), materialContentHash(material.String(), version.String()), h.user)
	// Even inconsistent registry family names cannot override identical audio/version.
	units, err := loadListeningFixtureUnits(t, h)
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range units {
		approveFixture(t, h, u, fmt.Sprintf("falsely-new-family-%d", i))
	}
	l := learningGet(t, h)
	var c Cycle
	for _, x := range l.Cycles {
		if x.Source.AttemptID == attempt.String() {
			c = x
			break
		}
	}
	if c.ID == "" || c.Source.AudioURL == nil {
		t.Fatal("listening source missing audio provenance")
	}
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "fix-audio", "north"), 200)
	for i, u := range units {
		s := startDrill(t, h, u.Drill.ID, fmt.Sprintf("same-origin-%d", i), &c)
		s = submitDrill(t, h, s, fmt.Sprintf("same-origin-submit-%d", i), "north", false)
		if s.Independent {
			t.Fatal("same original audio/question passed as new family")
		}
		c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	}
	if c.RemainingChecks != 2 {
		t.Fatal("same-origin retry reduced outstanding checks")
	}
	// Prior correct attempts without any cycle are also exposure, not a fresh control.
	h.sql(`DELETE FROM attempt_answers WHERE attempt_id=$1`, attempt)
	h.sql(`UPDATE attempts SET score=2 WHERE id=$1`, attempt)
	for _, q := range []uuid.UUID{q1, q2} {
		h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer,is_correct,points_awarded) VALUES($1,$2,'{"value":"north"}',true,1)`, attempt, q)
	}
	inTx, err := h.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer inTx.Rollback(t.Context())
	in, err := loadInputs(t.Context(), inTx, h.user)
	if err != nil {
		t.Fatal(err)
	}
	if !priorAttemptOrigin(in, units[0].Drill, h.now) {
		t.Fatal("previous successful attempt treated as unseen")
	}
	// Sibling questions from the same built-in passage stay one origin even if an
	// administrator supplied two different family labels.
	var first, second *drillUnit
	built := builtinDrills()
	for i := range built {
		if built[i].Drill.ID == "drill-r-01a-q1" {
			first = &built[i]
		}
		if built[i].Drill.ID == "drill-r-01a-q2" {
			second = &built[i]
		}
	}
	if first == nil || second == nil {
		t.Fatal("sibling fixture missing")
	}
	first.Drill.FamilyID = ptr("one")
	second.Drill.FamilyID = ptr("two")
	if !sameOrigin(first.Drill, second.Drill) {
		t.Fatal("sibling passage falsely independent")
	}
}
func loadListeningFixtureUnits(t *testing.T, h *harness) ([]drillUnit, error) {
	t.Helper()
	tx, err := h.pool.Begin(t.Context())
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(t.Context())
	return loadShortListening(t.Context(), tx, "academic")
}

func TestLearningSessionSubmissionConcurrencySkipAndNewCycle(t *testing.T) {
	h := setup(t)
	h.create(30)
	s := startDrill(t, h, "iac-r-library", "race-start", nil)
	input := map[string]any{"expectedRevision": s.Revision, "idempotencyKey": "race-submit", "text": "TRUE", "assisted": false}
	var wg sync.WaitGroup
	out := make(chan *httptest.ResponseRecorder, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out <- h.request("POST", "/study-plan/learning/sessions/"+s.ID+"/submit", input, h.token)
		}()
	}
	wg.Wait()
	close(out)
	for w := range out {
		if w.Code != 200 {
			t.Fatalf("same submission race failed %d %s", w.Code, w.Body)
		}
	}
	foreign := h.request("GET", "/study-plan/learning/sessions/"+s.ID, nil, h.otherToken)
	if foreign.Code != 404 {
		t.Fatal("foreign session visible")
	}
	input["idempotencyKey"] = "stale-submit"
	learningCall[Session](t, h, "POST", "/sessions/"+s.ID+"/submit", input, 409)
	l := learningGet(t, h)
	if len(l.Cycles) != 1 {
		t.Fatalf("race produced %d duplicate cycles", len(l.Cycles))
	}
	c := l.Cycles[0]
	learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/skip", map[string]any{"expectedRevision": c.Revision, "idempotencyKey": "skip-cycle"}, 200)
	learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "after-skip", "FALSE"), 409)
	next := startDrill(t, h, "iac-r-transport", "new-mistake", nil)
	submitDrill(t, h, next, "new-mistake-submit", "FALSE", false)
	l = learningGet(t, h)
	if len(l.Cycles) != 2 {
		t.Fatal("new distinct mistake did not create its own cycle")
	}
	h.now = h.now.AddDate(0, 0, 31)
	h.create(30)
	l = learningGet(t, h)
	if len(l.Cycles) != 2 {
		t.Fatal("calendar renewal lost learning history")
	}
	saved := learningCall[Session](t, h, "GET", "/sessions/"+s.ID, nil, 200)
	if saved.Status != "submitted" || saved.Text == nil || *saved.Text != "TRUE" {
		t.Fatal("original drill submission changed")
	}
	forged := h.request("POST", "/study-plan/learning/drills/iac-r-library/start", map[string]any{"expectedRevision": l.Revision, "idempotencyKey": "forged", "score": 1}, h.token)
	if forged.Code != 422 {
		t.Fatal("client score admitted")
	}
}

func TestLearningDelayedTransferUsesLocalCalendarHTTP(t *testing.T) {
	cases := []struct{ zone, anchor, early, delayed, nextDate string }{
		{"Pacific/Kiritimati", "2026-10-24T11:30:00Z", "2026-10-27T01:00:00Z", "2026-10-27T11:30:00Z", "2026-10-28"},
		{"Pacific/Honolulu", "2026-10-24T08:30:00Z", "2026-10-26T09:00:00Z", "2026-10-26T11:00:00Z", "2026-10-26"},
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			h := setup(t)
			h.now = mustTimestamp(tc.anchor)
			h.sql(`UPDATE user_profiles SET timezone=$2 WHERE user_id=$1`, h.user, tc.zone)
			h.create(30)
			units := builtinDrills()
			for _, id := range []string{"iac-r-library", "iac-r-transport", "iac-r-volunteer"} {
				u := unitByID(units, id)
				approveFixture(t, h, *u, *u.Drill.FamilyID)
			}
			s := startDrill(t, h, "iac-r-library", "timezone-source", nil)
			s = submitDrill(t, h, s, "timezone-source-submit", "TRUE", false)
			l := learningGet(t, h)
			var c Cycle
			for _, x := range l.Cycles {
				if x.Source.SessionID != nil && *x.Source.SessionID == s.ID {
					c = x
				}
			}
			c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "timezone-correction", "FALSE"), 200)
			if c.NextCheckDate == nil || *c.NextCheckDate != tc.nextDate {
				t.Fatalf("wrong local next date %+v", c.NextCheckDate)
			}
			h.now = mustTimestamp(tc.early)
			one := startDrill(t, h, "iac-r-transport", "early-calendar", &c)
			submitDrill(t, h, one, "early-calendar-submit", "NOT GIVEN", false)
			c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
			if len(c.Checks) != 1 || c.Checks[0].Delayed {
				t.Fatalf("UTC day incorrectly counted as local delayed check %+v", c.Checks)
			}
			h.now = mustTimestamp(tc.delayed)
			two := startDrill(t, h, "iac-r-volunteer", "delayed-calendar", &c)
			submitDrill(t, h, two, "delayed-calendar-submit", "TRUE", false)
			c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
			if c.Status != "demonstrated" || !c.Checks[1].Delayed {
				t.Fatalf("local day 3 failed %+v", c)
			}
		})
	}
}
