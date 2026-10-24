package studyplan

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func performanceRow(t *testing.T, l Learning, section, typ string) TaskPerformance {
	t.Helper()
	for _, p := range l.TaskPerformance {
		if p.Section == section && p.QuestionType == typ {
			return p
		}
	}
	t.Fatalf("missing %s/%s", section, typ)
	return TaskPerformance{}
}

func performanceReading(t *testing.T, h *harness) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	m, v, q := h.seedReading(20)
	h.sql(`UPDATE reading_material_versions SET body=$2 WHERE id=$1`, v, "Distinct passage "+m.String())
	return m, v, q
}

// Direct persisted fixtures exercise the projection's trust boundary. The first
// test also uses the real attempt submission/grade path for positive evidence.
func performanceAttempt(h *harness, owner uuid.UUID, section string, m, v uuid.UUID, when time.Time, status string, grades map[uuid.UUID]*bool) uuid.UUID {
	a := uuid.New()
	var submitted *time.Time
	if status == "SUBMITTED" {
		submitted = &when
	}
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,started_at,submitted_at) VALUES($1,$2,$3,$4,$5,$6,0,40,$7,$8)`, a, owner, section, m, v, status, when, submitted)
	for q, grade := range grades {
		h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer,is_correct) VALUES($1,$2,'{}',$3)`, a, q, grade)
	}
	return a
}

func assertPerformance(t *testing.T, p TaskPerformance, answered, correct, incorrect, sources, practice int) {
	t.Helper()
	if p.Answered != answered || p.Correct != correct || p.Incorrect != incorrect || p.SourceCount != sources || p.PracticeAnswered != practice || p.Answered != p.Correct+p.Incorrect {
		t.Fatalf("unexpected performance %+v; want answers/correct/incorrect/sources/practice %d/%d/%d/%d/%d", p, answered, correct, incorrect, sources, practice)
	}
	if p.SourceRefs == nil || p.Limitations == nil {
		t.Fatal("null arrays")
	}
}

func TestTaskPerformanceHTTPAllCorrectAndSingleErrorAreObservedFacts(t *testing.T) {
	h := setup(t)
	h.create(30)
	m, _, q := performanceReading(t, h)
	a, _, _, err := h.attempts.Start(t.Context(), h.user, "reading", m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.attempts.Submit(t.Context(), h.user, a.ID, answerInput(q, "TRUE")); err != nil {
		t.Fatal(err)
	}
	l := learningGet(t, h)
	p := performanceRow(t, l, "reading", "true_false_not_given")
	assertPerformance(t, p, 1, 1, 0, 1, 0)
	if p.SourceRefs[0].AttemptID != a.ID.String() || p.SourceRefs[0].Correct != 1 || len(l.Cycles) != 0 {
		t.Fatalf("all-correct evidence missing or fabricated error: %+v", l)
	}
	m2, _, q2 := performanceReading(t, h)
	a2, _, _, err := h.attempts.Start(t.Context(), h.user, "reading", m2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.attempts.Submit(t.Context(), h.user, a2.ID, answerInput(q2, "FALSE")); err != nil {
		t.Fatal(err)
	}
	l = learningGet(t, h)
	p = performanceRow(t, l, "reading", "true_false_not_given")
	assertPerformance(t, p, 2, 1, 1, 2, 0)
	if l.OverallBand != nil || len(l.Cycles) != 1 {
		t.Fatal("partial result inferred a band or lost concrete error")
	}
	var raw map[string]any
	body, _ := json.Marshal(p)
	_ = json.Unmarshal(body, &raw)
	for _, unsupported := range []string{"status", "band", "percentage", "mastery"} {
		if _, ok := raw[unsupported]; ok {
			t.Fatalf("unsupported claim %s", unsupported)
		}
	}
	// A saved correction and a real drill response belong to the micro-skill loop,
	// never the first-answer denominator, even when both are correct.
	c := l.Cycles[0]
	learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "performance-fix", "TRUE"), 200)
	s := startDrill(t, h, "iac-r-library", "performance-drill", nil)
	submitDrill(t, h, s, "performance-drill-submit", "FALSE", false)
	assertPerformance(t, performanceRow(t, learningGet(t, h), "reading", "true_false_not_given"), 2, 1, 1, 2, 0)
}

func TestTaskPerformanceHTTPReadingTestPassagesVersionsAndCopiedStimuli(t *testing.T) {
	h := setup(t)
	h.create(30)
	m1, v1, q1 := performanceReading(t, h)
	m2, v2, q2 := performanceReading(t, h)
	m3, v3, q3 := performanceReading(t, h)
	first := performanceAttempt(h, h.user, "reading", m1, v1, h.now.Add(-5*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q1: ptr(true)})
	test, version, _ := performanceReading(t, h)
	h.sql(`UPDATE reading_materials SET material_kind='TEST' WHERE id=$1`, test)
	for i, child := range [][2]uuid.UUID{{m1, v1}, {m2, v2}, {m3, v3}} {
		h.sql(`INSERT INTO reading_test_passages(test_material_version_id,position,passage_material_id,passage_material_version_id) VALUES($1,$2,$3,$4)`, version, i+1, child[0], child[1])
	}
	full := performanceAttempt(h, h.user, "reading", test, version, h.now.Add(-4*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q1: ptr(false), q2: ptr(false), q3: ptr(true)})
	l := learningGet(t, h)
	p := performanceRow(t, l, "reading", "true_false_not_given")
	assertPerformance(t, p, 3, 2, 1, 3, 1)
	for _, ref := range p.SourceRefs {
		if ref.Answered != 1 || (ref.AttemptID != first.String() && ref.AttemptID != full.String()) || ref.SourceID == "reading:"+test.String() {
			t.Fatalf("parent TEST counted as a fourth source: %+v", ref)
		}
	}
	performanceAttempt(h, h.user, "reading", test, version, h.now.Add(-3*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q1: ptr(true), q2: ptr(true), q3: ptr(true)})
	// A revised passage under the same stable material ID is still practice.
	newVersion, group, question := uuid.New(), uuid.New(), uuid.New()
	h.sql(`INSERT INTO reading_material_versions(id,material_id,version_number,title,body,created_by) VALUES($1,$2,2,'Revised passage','Changed wording of this passage.',$3)`, newVersion, m1, h.user)
	h.sql(`INSERT INTO reading_question_groups(id,material_version_id,position,question_type,created_by) VALUES($1,$2,1,'true_false_not_given',$3)`, group, newVersion, h.user)
	h.sql(`INSERT INTO reading_questions(id,group_id,position,prompt,answer,created_by) VALUES($1,$2,1,'Revised question','{"value":"TRUE"}',$3)`, question, group, h.user)
	performanceAttempt(h, h.user, "reading", m1, newVersion, h.now.Add(-2*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{question: ptr(true)})
	// The same passage copied to a new catalog ID also adds no new first text.
	copyM, copyV, copyQ := performanceReading(t, h)
	h.sql(`UPDATE reading_material_versions SET body=(SELECT body FROM reading_material_versions WHERE id=$2) WHERE id=$1`, copyV, v1)
	performanceAttempt(h, h.user, "reading", copyM, copyV, h.now.Add(-time.Hour), "SUBMITTED", map[uuid.UUID]*bool{copyQ: ptr(true)})
	assertPerformance(t, performanceRow(t, learningGet(t, h), "reading", "true_false_not_given"), 3, 2, 1, 3, 6)
}

func performanceListening(h *harness, material uuid.UUID, versionNumber int, audio uuid.UUID) (uuid.UUID, uuid.UUID, uuid.UUID) {
	version, part, group, q1, q2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if versionNumber == 1 {
		h.sql(`INSERT INTO listening_tests(id,slug,exam_type,created_by,updated_by) VALUES($1,$2,'academic',$3,$3)`, material, "performance-"+material.String(), h.user)
	}
	h.sql(`INSERT INTO listening_test_versions(id,test_id,version_number,title,created_by) VALUES($1,$2,$3,'Audio source',$4)`, version, material, versionNumber, h.user)
	h.sql(`INSERT INTO listening_parts(id,test_version_id,position,audio_asset_id) VALUES($1,$2,1,$3)`, part, version, audio)
	h.sql(`INSERT INTO listening_question_groups(id,part_id,position,question_type) VALUES($1,$2,1,'short_answer')`, group, part)
	for i, q := range []uuid.UUID{q1, q2} {
		h.sql(`INSERT INTO listening_questions(id,group_id,position,number,prompt,answer) VALUES($1,$2,$3,$3,'Which destination?','{"accepted":["north"]}')`, q, group, i+1)
	}
	return version, q1, q2
}

func TestTaskPerformanceHTTPListeningCountsWholeMaterialsAndFirstGradedAttempt(t *testing.T) {
	h := setup(t)
	h.create(30)
	material, audio := uuid.New(), uuid.New()
	h.sql(`INSERT INTO listening_media(id,kind,original_name,mime_type,storage_key,byte_size,created_by) VALUES($1,'audio','test.mp3','audio/mpeg',$2,100,$3)`, audio, "performance-"+audio.String(), h.user)
	v, q1, q2 := performanceListening(h, material, 1, audio)
	performanceAttempt(h, h.user, "listening", material, v, h.now.Add(-6*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q1: nil})
	first := performanceAttempt(h, h.user, "listening", material, v, h.now.Add(-5*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q1: ptr(true), q2: ptr(false)})
	p := performanceRow(t, learningGet(t, h), "listening", "short_answer")
	assertPerformance(t, p, 2, 1, 1, 1, 0)
	if p.Title != "Краткий ответ" || p.SourceRefs[0].AttemptID != first.String() || p.SourceRefs[0].SourceID != "listening:"+material.String() || p.SourceRefs[0].Answered != 2 || !strings.Contains(strings.Join(p.Limitations, " "), "одном источнике") {
		t.Fatalf("format turned into cognitive diagnosis or small source caveat lost %+v", p)
	}
	performanceAttempt(h, h.user, "listening", material, v, h.now.Add(-4*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q1: ptr(true), q2: ptr(true)})
	v2, n1, n2 := performanceListening(h, material, 2, audio)
	performanceAttempt(h, h.user, "listening", material, v2, h.now.Add(-3*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{n1: ptr(true), n2: ptr(true)})
	copyM := uuid.New()
	copyV, c1, c2 := performanceListening(h, copyM, 1, audio)
	performanceAttempt(h, h.user, "listening", copyM, copyV, h.now.Add(-2*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{c1: ptr(true), c2: ptr(true)})
	p = performanceRow(t, learningGet(t, h), "listening", "short_answer")
	assertPerformance(t, p, 2, 1, 1, 1, 6)
	if p.SourceRefs[0].SourceID != "listening:"+material.String() {
		t.Fatal("repeat/version/copy changed stable source ID")
	}
}

func TestTaskPerformanceHTTPOwnershipPendingHiddenMockAndQuestionMembership(t *testing.T) {
	h := setup(t)
	h.create(30)
	m, v, q := performanceReading(t, h)
	_, _, foreignQuestion := performanceReading(t, h)
	performanceAttempt(h, h.other, "reading", m, v, h.now.Add(-6*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q: ptr(false)})
	performanceAttempt(h, h.user, "reading", m, v, h.now.Add(-5*time.Hour), "IN_PROGRESS", map[uuid.UUID]*bool{q: ptr(false)})
	performanceAttempt(h, h.user, "reading", m, v, h.now.Add(-4*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q: nil})
	hidden := performanceAttempt(h, h.user, "reading", m, v, h.now.Add(-3*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q: ptr(false)})
	mock, session := uuid.New(), uuid.New()
	h.sql(`INSERT INTO full_mock_tests(id,slug,exam_type,title,listening_material_id,reading_material_id,writing_material_id,speaking_material_id) VALUES($1,$2,'academic','Hidden Mock',$3,$3,$3,$3)`, mock, "performance-"+mock.String(), m)
	h.sql(`INSERT INTO full_mock_sessions(id,mock_test_id,user_id,status) VALUES($1,$2,$3,'IN_PROGRESS')`, session, mock, h.user)
	h.sql(`INSERT INTO full_mock_session_sections(session_id,position,skill,attempt_id) VALUES($1,2,'reading',$2)`, session, hidden)
	assertPerformance(t, performanceRow(t, learningGet(t, h), "reading", "true_false_not_given"), 0, 0, 0, 0, 0)
	visible := performanceAttempt(h, h.user, "reading", m, v, h.now.Add(-2*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q: ptr(true), foreignQuestion: ptr(false), uuid.New(): ptr(false)})
	p := performanceRow(t, learningGet(t, h), "reading", "true_false_not_given")
	assertPerformance(t, p, 1, 1, 0, 1, 0)
	if p.SourceRefs[0].AttemptID != visible.String() {
		t.Fatal("hidden or foreign attempt established first exposure")
	}
	other := h.request("GET", "/study-plan/learning", nil, h.otherToken)
	var ol Learning
	if other.Code != 200 || json.Unmarshal(other.Body.Bytes(), &ol) != nil {
		t.Fatal(other.Body)
	}
	op := performanceRow(t, ol, "reading", "true_false_not_given")
	assertPerformance(t, op, 1, 0, 1, 1, 0)
	if op.SourceRefs[0].AttemptID == visible.String() {
		t.Fatal("owned review reference leaked")
	}
	// Completed Mock results become visible. They still share the same passage;
	// the later ordinary attempt becomes repeat practice, never another source.
	h.sql(`UPDATE full_mock_sessions SET status='SUBMITTED',submitted_at=$2 WHERE id=$1`, session, h.now)
	assertPerformance(t, performanceRow(t, learningGet(t, h), "reading", "true_false_not_given"), 1, 0, 1, 1, 1)
}

func TestTaskPerformanceHTTPUnknownFormatsBoundedRefsAndNoData(t *testing.T) {
	h := setup(t)
	h.create(30)
	l := learningGet(t, h)
	if len(l.TaskPerformance) != 25 || l.OverallBand != nil {
		t.Fatalf("empty catalog response %+v", l.TaskPerformance)
	}
	for _, p := range l.TaskPerformance {
		assertPerformance(t, p, 0, 0, 0, 0, 0)
	}
	// Future/legacy format labels are possible despite today's enum: simulate
	// them only inside this test's randomly isolated schema, without a migration.
	h.sql(`ALTER TABLE reading_question_groups DROP CONSTRAINT reading_question_groups_type_valid`)
	for _, typ := range []string{"", "future_format"} {
		m, v, q := performanceReading(t, h)
		h.sql(`UPDATE reading_question_groups SET question_type=$2 WHERE material_version_id=$1`, v, typ)
		performanceAttempt(h, h.user, "reading", m, v, h.now.Add(-time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q: ptr(true)})
	}
	for i := 0; i < 7; i++ {
		m, v, q := performanceReading(t, h)
		performanceAttempt(h, h.user, "reading", m, v, h.now.Add(time.Duration(i-10)*time.Hour), "SUBMITTED", map[uuid.UUID]*bool{q: ptr(i%2 == 0)})
	}
	l = learningGet(t, h)
	for _, typ := range []string{"unknown", "future_format"} {
		p := performanceRow(t, l, "reading", typ)
		assertPerformance(t, p, 1, 1, 0, 1, 0)
		if !strings.HasPrefix(p.Title, "Неизвестный тип задания") {
			t.Fatalf("unreadable unknown %+v", p)
		}
	}
	p := performanceRow(t, l, "reading", "true_false_not_given")
	assertPerformance(t, p, 7, 4, 3, 7, 0)
	if len(p.SourceRefs) != performanceRefLimit || p.SourceRefs[0].OccurredAt < p.SourceRefs[4].OccurredAt {
		t.Fatalf("unbounded/unsorted review refs %+v", p.SourceRefs)
	}
	for _, limit := range p.Limitations {
		for _, technical := range []string{"answered/correct", "sourceRefs", "sourceCount", "practiceAnswered"} {
			if strings.Contains(limit, technical) {
				t.Fatalf("technical key in learner text %q", limit)
			}
		}
	}
}

func TestTaskPerformanceDoesNotChangePlannerFingerprint(t *testing.T) {
	in := inputs{}
	before := fingerprint(in)
	in.TaskPerformance = taskPerformance(nil)
	if fingerprint(in) != before {
		t.Fatal("format observations became a planner input")
	}
}
