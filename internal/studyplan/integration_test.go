package studyplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/almatkai/ielts-after-cigarette-back/internal/reading"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type harness struct {
	t          *testing.T
	pool       *pgxpool.Pool
	user       uuid.UUID
	other      uuid.UUID
	service    *Service
	attempts   *attempts.Service
	router     http.Handler
	token      string
	otherToken string
	now        time.Time
}

func setup(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, pool: testdb.Open(t), now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	h.user = testdb.User(t, h.pool)
	h.other = testdb.User(t, h.pool)
	h.sql(`INSERT INTO user_profiles(user_id,display_name,timezone,exam_type,target_band) VALUES($1,'Test student','UTC','academic',7)`, h.user)
	h.sql(`INSERT INTO user_profiles(user_id,display_name,timezone,exam_type) VALUES($1,'Other student','UTC','academic')`, h.other)
	rs := reading.NewService(reading.NewPostgresRepository(h.pool))
	h.attempts = attempts.NewService(attempts.NewPostgresRepository(h.pool), map[string]attempts.MaterialProvider{"reading": attempts.NewReadingProvider(rs)})
	h.service = NewService(h.pool, h.attempts, fullmock.NewPostgresRepository(h.pool))
	h.service.now = func() time.Time { return h.now }
	tm := auth.NewTokenManager("isolated-study-plan-test-secret", "tests", "tests", time.Hour, time.Hour)
	var e error
	h.token, _, e = tm.NewAccessToken(h.user, auth.RoleStudent)
	if e != nil {
		t.Fatal(e)
	}
	h.otherToken, _, e = tm.NewAccessToken(h.other, auth.RoleStudent)
	if e != nil {
		t.Fatal(e)
	}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Authenticate(tm))
		NewHandler(h.service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20).Routes(r)
	})
	h.router = r
	return h
}
func (h *harness) sql(query string, args ...any) {
	h.t.Helper()
	if _, e := h.pool.Exec(context.Background(), query, args...); e != nil {
		h.t.Fatal(e)
	}
}
func (h *harness) request(method, path string, input any, token string) *httptest.ResponseRecorder {
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	out := httptest.NewRecorder()
	h.router.ServeHTTP(out, req)
	return out
}
func (h *harness) call(method, path string, input any, status int) Response {
	h.t.Helper()
	w := h.request(method, path, input, h.token)
	if w.Code != status {
		h.t.Fatalf("%s %s: expected %d got %d: %s", method, path, status, w.Code, w.Body)
	}
	var out Response
	if status == 200 {
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			h.t.Fatal(e)
		}
	}
	return out
}
func (h *harness) create(minutes int) Response {
	return h.call("POST", "/study-plan", allDays(minutes), 200)
}
func (h *harness) seedReading(minutes int) (uuid.UUID, uuid.UUID, uuid.UUID) {
	h.t.Helper()
	id, version, group, question := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	h.sql(`INSERT INTO reading_materials(id,slug,exam_type,difficulty,created_by,updated_by) VALUES($1,$2,'academic','intermediate',$3,$3)`, id, "test-"+id.String(), h.user)
	h.sql(`INSERT INTO reading_material_versions(id,material_id,version_number,title,body,created_by,duration_minutes) VALUES($1,$2,1,'Published real passage','A real test passage body.',$3,$4)`, version, id, h.user, minutes)
	h.sql(`INSERT INTO reading_question_groups(id,material_version_id,position,question_type,created_by) VALUES($1,$2,1,'true_false_not_given',$3)`, group, version, h.user)
	h.sql(`INSERT INTO reading_questions(id,group_id,position,prompt,answer,created_by) VALUES($1,$2,1,'The statement is true.','{"value":"TRUE"}',$3)`, question, group, h.user)
	h.sql(`UPDATE reading_materials SET status='PUBLISHED',current_version_id=$2,published_version_id=$2,published_at=CURRENT_TIMESTAMP,published_by=$3 WHERE id=$1`, id, version, h.user)
	return id, version, question
}
func assignmentOn(t *testing.T, r Response, day, kind string) Assignment {
	t.Helper()
	for _, d := range r.Plan.Days {
		if d.Date != day {
			continue
		}
		for _, a := range d.Assignments {
			if kind == "" || a.Kind == kind {
				return a
			}
		}
	}
	t.Fatalf("no %s assignment for %s", kind, day)
	return Assignment{}
}
func TestHandlerAuthCreationOwnershipEmptyBankAndValidation(t *testing.T) {
	h := setup(t)
	withdrawBuiltinPractice(h)
	if w := h.request("GET", "/study-plan", nil, ""); w.Code != 401 {
		t.Fatal("unauthenticated plan exposed")
	}
	r := h.call("GET", "/study-plan", nil, 200)
	if r.Plan != nil || len(r.Evidence) != 4 || len(r.Progress) != 0 || r.OverallBand != nil {
		t.Fatalf("invalid empty response %+v", r)
	}
	h.call("POST", "/study-plan", Settings{DailyMinutes: 30, StudyDays: []int{1, 1}}, 422)
	r = h.create(30)
	if len(r.Plan.Days) != 30 || r.Goal.TargetBand == nil || r.Goal.ExamType == nil {
		t.Fatalf("bad plan %+v", r)
	}
	id, rev := r.Plan.ID, r.Plan.Revision
	for _, d := range r.Plan.Days {
		if len(d.Assignments) != 1 || d.Assignments[0].Status != "BLOCKED" || d.PlannedMinutes != 0 {
			t.Fatal("empty-bank fabricated task")
		}
	}
	for i := 0; i < 2; i++ {
		next := h.call("GET", "/study-plan?date=2040-01-01", nil, 200)
		if next.Plan.Revision != rev || next.Today != r.Today {
			t.Fatal("unstable/client-clock GET")
		}
	}
	next := h.create(60)
	if next.Plan.ID != id || next.Settings.DailyMinutes != 30 || next.Plan.Revision != rev {
		t.Fatal("create destroyed cycle")
	}
	foreign := h.request("POST", "/study-plan", allDays(30), h.otherToken)
	if foreign.Code != 200 {
		t.Fatal(foreign.Body)
	}
	a := r.Plan.Days[0].Assignments[0]
	w := h.request("POST", "/study-plan/assignments/"+a.ID+"/skip", map[string]any{"expectedRevision": 1, "reason": "no_time"}, h.otherToken)
	if w.Code != 404 {
		t.Fatalf("foreign assignment accepted %d", w.Code)
	}
	start := h.request("POST", "/study-plan/assignments/"+a.ID+"/start", mutationInput{ExpectedRevision: rev}, h.token)
	if start.Code != 200 {
		t.Fatal(start.Body)
	}
	var sr StartResponse
	_ = json.Unmarshal(start.Body.Bytes(), &sr)
	if sr.AttemptID != nil || sr.Target != "/practice" {
		t.Fatal("blocked start fabricated attempt")
	}
	h.call("POST", "/study-plan/replan", mutationInput{ExpectedRevision: 0}, 409)
}
func TestHandlerExistingDraftStartConcurrencyAndRealCompletion(t *testing.T) {
	h := setup(t)
	material, _, question := h.seedReading(20)
	draft, _, _, e := h.attempts.Start(context.Background(), h.user, "reading", material)
	if e != nil {
		t.Fatal(e)
	}
	h.sql(`UPDATE attempts SET started_at=$2 WHERE id=$1`, draft.ID, h.now)
	r := h.create(30)
	a := assignmentOn(t, r, r.Today, "")
	input := mutationInput{ExpectedRevision: r.Plan.Revision}
	path := "/study-plan/assignments/" + a.ID + "/start"
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- h.request("POST", path, input, h.token) }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for w := range results {
		if w.Code == 200 {
			successes++
			var sr StartResponse
			_ = json.Unmarshal(w.Body.Bytes(), &sr)
			if sr.AttemptID == nil || *sr.AttemptID != draft.ID.String() || sr.Target != "/exam/reading/"+material.String() {
				t.Fatalf("wrong draft navigation %+v", sr)
			}
		} else if w.Code != 409 {
			t.Fatalf("concurrent start %d: %s", w.Code, w.Body)
		}
	}
	if successes != 1 {
		t.Fatalf("%d successful first starts", successes)
	}
	r = h.call("GET", "/study-plan", nil, 200)
	a = assignmentOn(t, r, r.Today, "")
	if a.Status != "IN_PROGRESS" || a.AttemptID == nil || !strings.Contains(a.Reason, "Продолжение вашей начатой попытки") {
		t.Fatalf("not explicit resume %+v", a)
	}
	for i := 0; i < 2; i++ {
		w := h.request("POST", path, mutationInput{ExpectedRevision: r.Plan.Revision}, h.token)
		if w.Code != 200 {
			t.Fatal(w.Body)
		}
	}
	var count int
	_ = h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM attempts WHERE user_id=$1`, h.user).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate attempts")
	}
	// Real grading through the existing service. No checkbox or supplied success score.
	_, e = h.attempts.Submit(context.Background(), h.user, draft.ID, attempts.SaveAnswersInput{Answers: []attempts.AnswerInput{{QuestionID: question, Answer: map[string]any{"value": "FALSE"}}}})
	if e != nil {
		t.Fatal(e)
	}
	h.sql(`UPDATE attempts SET submitted_at=$2 WHERE id=$1`, draft.ID, h.now)
	r = h.call("GET", "/study-plan", nil, 200)
	a = assignmentOn(t, r, r.Today, "")
	if a.Status != "COMPLETED" || a.CompletedAt == nil || len(r.Recommendations) != 1 || r.Recommendations[0].AttemptID == nil {
		t.Fatalf("real result not projected %+v", r)
	}
	if r.Progress[0].CompletedAttempts != 1 || r.Progress[0].ActiveMinutes != nil || r.OverallBand != nil {
		t.Fatalf("fabricated progress %+v", r.Progress[0])
	}
	rev := r.Plan.Revision
	r = h.call("GET", "/study-plan", nil, 200)
	if r.Plan.Revision != rev {
		t.Fatal("result GET keeps incrementing")
	}
	h.call("POST", "/study-plan/assignments/"+a.ID+"/skip", map[string]any{"expectedRevision": rev, "reason": "outside_app"}, 409)
}
func TestHandlerSkipsReplansSettingsAndReadonlyDates(t *testing.T) {
	h := setup(t)
	for i := 0; i < 4; i++ {
		h.seedReading(20)
	}
	r := h.create(60)
	a := assignmentOn(t, r, r.Today, "")
	oldDay := r.Plan.Days[0]
	h.call("POST", "/study-plan/assignments/"+r.Plan.Days[1].Assignments[0].ID+"/start", mutationInput{ExpectedRevision: r.Plan.Revision}, 409)
	h.call("POST", "/study-plan/assignments/"+a.ID+"/skip", map[string]any{"expectedRevision": r.Plan.Revision, "reason": "invalid"}, 422)
	rev := r.Plan.Revision
	r = h.call("POST", "/study-plan/assignments/"+a.ID+"/skip", map[string]any{"expectedRevision": rev, "reason": "outside_app"}, 200)
	h.call("POST", "/study-plan/replan", mutationInput{ExpectedRevision: rev}, 409)
	rev = r.Plan.Revision
	r = h.call("POST", "/study-plan/assignments/"+a.ID+"/skip", map[string]any{"expectedRevision": rev, "reason": "no_time"}, 200)
	if r.Plan.Revision != rev || *r.Plan.Days[0].Assignments[0].SkipReason != "outside_app" || r.Progress[0].CompletedAttempts != 0 {
		t.Fatal("skip not idempotent or false outside-app credit")
	}
	r = h.call("PUT", "/study-plan/settings", map[string]any{"expectedRevision": rev, "dailyMinutes": 30, "studyDays": []int{1, 2, 3, 4, 5}, "weeklyMockEnabled": false}, 200)
	if r.Plan.Days[0].BudgetMinutes != oldDay.BudgetMinutes || len(r.Plan.Days[0].Assignments) != len(oldDay.Assignments) {
		t.Fatal("settings rewrote today")
	}
	r = h.call("POST", "/study-plan/replan", mutationInput{ExpectedRevision: r.Plan.Revision}, 200)
	if r.Plan.Days[0].Assignments[0].ID != a.ID || r.Plan.Days[0].Assignments[0].Status != "SKIPPED" {
		t.Fatal("replan erased skip")
	}
	for _, d := range r.Plan.Days[1:] {
		if d.PlannedMinutes > d.BudgetMinutes || len(d.Assignments) > 3 {
			t.Fatalf("doubled day %+v", d)
		}
		if d.IsRest && len(d.Assignments) != 0 {
			t.Fatal("rest has work")
		}
	}
}
func TestHandlerPauseCalendarReentryAndDST(t *testing.T) {
	for _, days := range []int{6, 7, 14} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			h := setup(t)
			h.seedReading(20)
			h.sql(`UPDATE user_profiles SET timezone='America/New_York' WHERE user_id=$1`, h.user)
			zone, _ := time.LoadLocation("America/New_York")
			h.now = time.Date(2026, 10, 31, 23, 30, 0, 0, zone)
			initial := h.create(30)
			h.now = h.now.AddDate(0, 0, days)
			r := h.call("GET", "/study-plan", nil, 200)
			if r.Today != addDays(initial.Today, days) || r.Plan.ReturnAfterPause != (days >= 7) {
				t.Fatalf("pause calendar %+v", r.Plan)
			}
			for _, d := range r.Plan.Days {
				if d.Date < r.Today {
					for _, a := range d.Assignments {
						if a.Status != "MISSED" {
							t.Fatal("past unstarted not missed")
						}
					}
				}
			}
			a := assignmentOn(t, r, r.Today, "")
			if days >= 7 && (a.Kind != "REENTRY" || a.MaterialID == nil || a.DurationMinutes > 30) {
				t.Fatalf("not real small reentry %+v", a)
			}
			rev := r.Plan.Revision
			r = h.call("GET", "/study-plan", nil, 200)
			if rev != r.Plan.Revision {
				t.Fatal("same-day reconciliation unstable")
			}
		})
	}
}
func TestHandlerReviewAcknowledgementOnlyAndDelayedLocalReview(t *testing.T) {
	h := setup(t)
	material, version, q := h.seedReading(20)
	id := uuid.New()
	old := h.now.AddDate(0, 0, -3)
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,started_at,submitted_at,band) VALUES($1,$2,'reading',$3,$4,'SUBMITTED',0,1,$5,$5,0)`, id, h.user, material, version, old)
	h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer,is_correct,points_awarded) VALUES($1,$2,'{"value":"FALSE"}',false,0)`, id, q)
	r := h.create(30)
	a := assignmentOn(t, r, r.Today, "REVIEW")
	start := h.request("POST", "/study-plan/assignments/"+a.ID+"/start", mutationInput{ExpectedRevision: r.Plan.Revision}, h.token)
	if start.Code != 200 {
		t.Fatal(start.Body)
	}
	var sr StartResponse
	_ = json.Unmarshal(start.Body.Bytes(), &sr)
	if sr.AttemptID == nil || *sr.AttemptID != id.String() || sr.Target != "/attempts/"+id.String() {
		t.Fatal("review points to wrong attempt")
	}
	r = h.call("GET", "/study-plan", nil, 200)
	if r.Progress[0].ReviewedAssignments != 0 {
		t.Fatal("start counted as viewed")
	}
	r = h.call("POST", "/study-plan/assignments/"+a.ID+"/review", mutationInput{ExpectedRevision: r.Plan.Revision}, 200)
	if r.Progress[0].ReviewedAssignments != 1 || r.Progress[0].CompletedAttempts != 0 || r.Evidence[1].SampleCount != 1 || r.Evidence[1].Band != nil {
		t.Fatal("view treated as mastery")
	}
	rev := r.Plan.Revision
	r = h.call("POST", "/study-plan/assignments/"+a.ID+"/review", mutationInput{ExpectedRevision: rev}, 200)
	if r.Plan.Revision != rev || r.Progress[0].ReviewedAssignments != 1 {
		t.Fatal("review not idempotent")
	}
}
func TestHandlerWeeklyMockDurationDateDeclineAndNoSessions(t *testing.T) {
	for _, action := range []string{"schedule", "decline"} {
		t.Run(action, func(t *testing.T) {
			h := setup(t)
			h.seedReading(20)
			r := h.create(45)
			h.call("POST", "/study-plan/weeks/1/mock", map[string]any{"expectedRevision": r.Plan.Revision, "action": action, "scheduledDate": "2026-11-03", "windowMinutes": 165}, 409)
			h.now = h.now.AddDate(0, 0, 6)
			r = h.call("GET", "/study-plan", nil, 200)
			if action == "schedule" {
				for _, input := range []map[string]any{{"expectedRevision": r.Plan.Revision, "action": "schedule", "scheduledDate": "2026-11-03", "windowMinutes": 164}, {"expectedRevision": r.Plan.Revision, "action": "schedule", "scheduledDate": "2026-11-09", "windowMinutes": 165}} {
					h.call("POST", "/study-plan/weeks/1/mock", input, 422)
				}
			}
			input := map[string]any{"expectedRevision": r.Plan.Revision, "action": action, "scheduledDate": "2026-11-03", "windowMinutes": r.Plan.Weeks[0].MockOffer.DurationMinutes}
			r = h.call("POST", "/study-plan/weeks/1/mock", input, 200)
			status := "DECLINED"
			if action == "schedule" {
				status = "SCHEDULED"
			}
			if r.Plan.Weeks[0].MockOffer.Status != status {
				t.Fatal("decision lost")
			}
			rev := r.Plan.Revision
			input["expectedRevision"] = rev
			r = h.call("POST", "/study-plan/weeks/1/mock", input, 200)
			if r.Plan.Revision != rev {
				t.Fatal("mock decision not idempotent")
			}
			r = h.call("POST", "/study-plan/replan", mutationInput{ExpectedRevision: rev}, 200)
			if r.Plan.Weeks[0].MockOffer.Status != status {
				t.Fatal("replan reset decision")
			}
			if action == "schedule" {
				for _, d := range r.Plan.Days {
					if d.Date == "2026-11-03" {
						if d.BudgetMinutes != 165 || d.PlannedMinutes != 0 {
							t.Fatal("ordinary work returned on long day")
						}
						for _, a := range d.Assignments {
							if a.Status != "SKIPPED" {
								t.Fatal("superseded task resumed")
							}
						}
					}
				}
			}
			var count int
			_ = h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM full_mock_sessions`).Scan(&count)
			if count != 0 {
				t.Fatal("scheduling created session")
			}
		})
	}
}
func TestHandlerPendingLatestRepeatedVersionsAndGTBank(t *testing.T) {
	h := setup(t)
	material, version, _ := h.seedReading(20)
	old := h.now.AddDate(0, 0, -35)
	recent := h.now.AddDate(0, 0, -1)
	for i, at := range []time.Time{old, recent} {
		score := 1 - i
		h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,band,started_at,submitted_at) VALUES($1,$2,'reading',$3,$4,'SUBMITTED',$5,1,$6,$7,$7)`, uuid.New(), h.user, material, func() uuid.UUID {
			if i == 0 {
				return version
			}
			return uuid.New()
		}(), score, float64(score)*9, at)
	}
	pending := uuid.New()
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,started_at) VALUES($1,$2,'reading',$3,$4,'PROCESSING',$5)`, pending, h.user, uuid.New(), uuid.New(), h.now)
	r := h.create(30)
	e := r.Evidence[1]
	if e.Status != "AWAITING_RESULT" || e.PracticeBand != nil || e.Score == nil || *e.Score != 0 || e.SampleCount != 1 || e.AssessedAt == nil || e.Band != nil {
		t.Fatalf("pending/repeats inflated evidence %+v", e)
	}
	h.sql(`UPDATE user_profiles SET exam_type='general' WHERE user_id=$1`, h.user)
	r = h.call("GET", "/study-plan", nil, 200)
	for _, d := range r.Plan.Days[1:] {
		for _, a := range d.Assignments {
			if a.MaterialID != nil {
				t.Fatal("Academic material silently substituted for GT")
			}
		}
	}
}

// Catalog-only legacy regressions deliberately withdraw the optional built-in
// drafts, so their small-bank/empty-bank assumptions remain explicit fixtures.
func withdrawBuiltinPractice(h *harness) {
	for _, u := range builtinDrills() {
		h.sql(`INSERT INTO study_learning_content_metadata(kind,reference_id,version,section,family_id,status,content_hash,revision,review_notes,reviewer_id,reviewed_at) VALUES('drill',$1,$2,$3,'','withdrawn',$4,1,'Catalog-only isolated regression fixture',$5,CURRENT_TIMESTAMP) ON CONFLICT(kind,reference_id,version) DO UPDATE SET status='withdrawn'`, u.Drill.ID, u.Drill.Version, u.Drill.Section, drillContentHash(u), h.user)
	}
}
