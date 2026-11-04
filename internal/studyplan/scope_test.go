package studyplan

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestScopedHTTPProjection(t *testing.T) {
	h := setup(t)
	full := h.call("POST", "/study-plan", defaultSettings(), 200)
	for _, view := range []string{"day", "week", "month"} {
		selected := addDays(full.Plan.StartDate, 8)
		scoped := h.call("GET", "/study-plan?view="+view+"&date="+selected, nil, 200)
		expected := 1
		if view == "week" {
			expected = 7
		}
		if view == "month" {
			expected = 30
		}
		if len(scoped.Plan.Days) != expected || len(scoped.Progress) != expected {
			t.Fatalf("%s: days=%d progress=%d", view, len(scoped.Plan.Days), len(scoped.Progress))
		}
		if scoped.Scope == nil || scoped.Scope.Date != selected || scoped.Today != full.Today || scoped.LearningSummary == nil {
			t.Fatalf("missing scope/clock/summary: %+v", scoped)
		}
		for _, day := range scoped.Plan.Days {
			found := false
			for _, original := range full.Plan.Days {
				if original.Date == day.Date {
					found = true
					if !reflect.DeepEqual(original, day) {
						t.Fatalf("%s day mismatch", view)
					}
				}
			}
			if !found {
				t.Fatal("unexpected day")
			}
		}
		for _, row := range scoped.Progress {
			for _, original := range full.Progress {
				if row.Date == original.Date && !reflect.DeepEqual(row, original) {
					t.Fatal("progress differs from full")
				}
			}
		}
		if len(scoped.Evidence) != 0 || len(scoped.Recommendations) != 0 || len(scoped.Plan.Changes) != 0 {
			t.Fatal("initial scope projects details")
		}
		if scoped.Scope.NextStudyDate != nil && *scoped.Scope.NextStudyDate <= scoped.Scope.EndDate {
			t.Fatal("next study date must follow loaded range")
		}
		if scoped.Plan.Revision != full.Plan.Revision {
			t.Fatal("read changed revision")
		}
	}
	detail := h.call("GET", "/study-plan?view=day&details=1", nil, 200)
	if !reflect.DeepEqual(detail.Evidence, full.Evidence) || !reflect.DeepEqual(detail.Plan.Changes, full.Plan.Changes) {
		t.Fatal("details lost history")
	}
	final := h.call("GET", "/study-plan?view=week&date="+full.Plan.EndDate, nil, 200)
	if len(final.Plan.Days) != 2 {
		t.Fatal("final partial week")
	}
	empty := h.call("GET", "/study-plan?view=day&date=2027-01-01", nil, 200)
	if len(empty.Plan.Days) != 0 || empty.Scope.Date != "2027-01-01" || empty.Today != full.Today {
		t.Fatal("out of range overrides clock or leaks days")
	}
	other := h.request("GET", "/study-plan?view=month", nil, h.otherToken)
	var foreign Response
	_ = json.Unmarshal(other.Body.Bytes(), &foreign)
	if other.Code != 200 || foreign.Plan != nil || len(foreign.Progress) != 0 {
		t.Fatal("foreign aggregate leak")
	}
	if got := h.request("GET", "/study-plan?view=day", nil, "").Code; got != 401 {
		t.Fatalf("unauthenticated %d", got)
	}
	// A projection date never authorizes a future assignment write.
	future := full.Plan.Days[1]
	if len(future.Assignments) > 0 {
		h.call("POST", "/study-plan/assignments/"+future.Assignments[0].ID+"/skip", map[string]any{"expectedRevision": full.Plan.Revision, "reason": "no_time"}, 409)
	}
	day := full.Plan.Days[0]
	if len(day.Assignments) > 0 {
		mutated := h.call("POST", "/study-plan/assignments/"+day.Assignments[0].ID+"/skip", map[string]any{"expectedRevision": full.Plan.Revision, "reason": "no_time"}, 200)
		fresh := h.call("GET", "/study-plan?view=day", nil, 200)
		if fresh.Plan.Revision != mutated.Plan.Revision || fresh.Plan.Days[0].Assignments[0].Status != "SKIPPED" {
			t.Fatal("stale scoped mutation")
		}
		h.call("POST", "/study-plan/replan", map[string]any{"expectedRevision": full.Plan.Revision}, 409)
	}
	h.now = h.now.AddDate(0, 0, 35)
	expired := h.call("GET", "/study-plan?view=month", nil, 200)
	if len(expired.Plan.Days) != 30 || expired.Plan.ID != full.Plan.ID {
		t.Fatal("expired cycle was lost")
	}
}
func TestScopeValidationHTTP(t *testing.T) {
	h := setup(t)
	for _, q := range []string{"view=", "view=year", "view=%zz", "view=day;view=month", "view=day&view=week", "view=day&view=day", "view=day&date=2026-02-30", "view=day&date=2026-1-01", "view=day&date=", "view=day&date=2026-01-01&date=2026-01-02", "date=invalid", "details=1", "view=day&details=0", "view=day&details=1&details=1"} {
		if w := h.request("GET", "/study-plan?"+q, nil, h.token); w.Code != 422 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body)
		}
	}
}
func TestScopedServerTodayTimezoneDST(t *testing.T) {
	h := setup(t)
	h.sql(`UPDATE user_profiles SET timezone='America/New_York' WHERE user_id=$1`, h.user)
	h.now = time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC)
	full := h.call("POST", "/study-plan", defaultSettings(), 200)
	if full.Today != "2026-10-31" {
		t.Fatal(full.Today)
	}
	for _, stamp := range []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-11-02T04:30:00Z"} {
		h.now, _ = time.Parse(time.RFC3339, stamp)
		out := h.call("GET", "/study-plan?view=day", nil, 200)
		if out.Today != "2026-11-01" || out.Scope.Date != out.Today || len(out.Plan.Days) != 1 || out.Plan.Days[0].Date != out.Today {
			t.Fatalf("%s: %+v", stamp, out.Scope)
		}
	}
	h.sql(`UPDATE user_profiles SET exam_date='2026-11-01' WHERE user_id=$1`, h.user)
	scoped := h.call("GET", "/study-plan?view=week&date=2026-11-01", nil, 200)
	if !scoped.LearningSummary.ReadOnly {
		t.Fatal("exam deadline ignored")
	}
	for _, d := range scoped.Plan.Days {
		if d.Date >= "2026-11-01" && d.ReadOnlyReason == nil {
			t.Fatal("exam boundary")
		}
	}
}
func TestCompactLearningAndProjectionIsolation(t *testing.T) {
	st := state{Plan: makePlan("2026-10-22", defaultSettings(), 165)}
	st.Learning.Cycles = []learningCycle{
		{Cycle: Cycle{Status: "needs-practice"}},
		{Cycle: Cycle{Status: "needs-practice", SkippedAt: ptr("2026-10-22")}},
		{Cycle: Cycle{Status: "practicing", Correction: &Correction{Verdict: "incorrect"}}},
	}
	summary := compactLearning(&st, Goal{}, "2026-10-22")
	full := learningResponse(&st, inputs{}, "2026-10-22")
	count := 0
	for _, c := range full.Cycles {
		if c.SkippedAt == nil && (c.Status == "needs-practice" || (c.Correction != nil && c.Correction.Verdict == "incorrect")) {
			count++
		}
	}
	if summary.PendingCorrections != count || summary.ReadOnly != full.ReadOnly || count != 2 {
		t.Fatal("summary differs from Learning")
	}
	// Projecting must not truncate persisted days or changes.
	before, _ := json.Marshal(st)
	s, _ := parseScope(url.Values{"view": {"day"}, "date": {"2026-10-23"}})
	plan, scope := projectPlan(&st.Plan, s, "2026-10-22")
	after, _ := json.Marshal(st)
	if len(plan.Days) != 1 || scope.StartDate != "2026-10-23" || string(before) != string(after) {
		t.Fatal("projection mutated aggregate")
	}
}

func TestScopedHTTPCompactLearningAndHistory(t *testing.T) {
	h := setup(t)
	h.create(30)
	cycle := sourceCycle(t, h)
	assertSummary := func() {
		t.Helper()
		scoped := h.call("GET", "/study-plan?view=day", nil, 200)
		full := learningGet(t, h)
		count := 0
		for _, c := range full.Cycles {
			if c.SkippedAt == nil && (c.Status == "needs-practice" || (c.Correction != nil && c.Correction.Verdict == "incorrect")) {
				count++
			}
		}
		if scoped.LearningSummary.PendingCorrections != count || scoped.LearningSummary.ReadOnly != full.ReadOnly {
			t.Fatalf("summary=%+v full count=%d", scoped.LearningSummary, count)
		}
	}
	assertSummary()
	cycle = learningCall[Cycle](t, h, "POST", "/cycles/"+cycle.ID+"/correction", correctionInput(cycle, "scoped-wrong", "not correct"), 200)
	assertSummary()
	learningCall[Cycle](t, h, "POST", "/cycles/"+cycle.ID+"/skip", map[string]any{"expectedRevision": cycle.Revision, "idempotencyKey": "scoped-skip"}, 200)
	assertSummary()
	full := h.call("GET", "/study-plan", nil, 200)
	full = h.call("POST", "/study-plan/replan", map[string]any{"expectedRevision": full.Plan.Revision}, 200)
	if len(full.Plan.Changes) == 0 {
		t.Fatal("fixture must include real history")
	}
	scoped := h.call("GET", "/study-plan?view=day", nil, 200)
	detail := h.call("GET", "/study-plan?view=day&details=1", nil, 200)
	if len(scoped.Plan.Changes) != 0 || !reflect.DeepEqual(full.Plan.Changes, detail.Plan.Changes) {
		t.Fatal("scoping lost history")
	}
}
