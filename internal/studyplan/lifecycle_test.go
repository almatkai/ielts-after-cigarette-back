package studyplan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestExplicitExpiredCycleRetainsHistoryAndAttemptClaims(t *testing.T) {
	h := setup(t)
	h.seedReading(20)
	r := h.create(30)
	a := assignmentOn(t, r, r.Today, "")
	r = h.call("POST", "/study-plan/assignments/"+a.ID+"/skip", map[string]any{"expectedRevision": r.Plan.Revision, "reason": "outside_app"}, 200)
	if strings.Contains(r.Plan.Changes[len(r.Plan.Changes)-1].Description, "outside_app") {
		t.Fatal("raw skip reason leaked into public copy")
	}
	oldStart := r.Plan.StartDate
	h.now = h.now.AddDate(0, 0, 31)
	expired := h.call("GET", "/study-plan", nil, 200)
	if expired.Plan.StartDate != oldStart {
		t.Fatal("GET silently replaced expired cycle")
	}
	next := h.create(45)
	if next.Plan.StartDate != next.Today || len(next.Plan.Days) != 30 || next.Plan.ID != expired.Plan.ID || next.Plan.Revision != expired.Plan.Revision+1 {
		t.Fatalf("invalid explicit next cycle %+v", next.Plan)
	}
	var raw []byte
	if e := h.pool.QueryRow(context.Background(), `SELECT state FROM study_plans WHERE user_id=$1`, h.user).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var st state
	if e := json.Unmarshal(raw, &st); e != nil {
		t.Fatal(e)
	}
	if len(st.History) != 1 || st.History[0].StartDate != oldStart || st.History[0].Days[0].Assignments[0].ID != a.ID || st.History[0].Days[0].Assignments[0].Status != "SKIPPED" || *st.History[0].Days[0].Assignments[0].SkipReason != "outside_app" {
		t.Fatal("expired cycle history lost")
	}
	repeat := h.create(90)
	if repeat.Plan.Revision != next.Plan.Revision || repeat.Settings.DailyMinutes != 45 {
		t.Fatal("active cycle recreated")
	}
}
func TestReentryOnRestDayHasRealShortMaterialOrBlocked(t *testing.T) {
	for _, hasBank := range []bool{false, true} {
		h := setup(t)
		withdrawBuiltinPractice(h)
		if hasBank {
			h.seedReading(20)
		}
		settings := Settings{DailyMinutes: 30, StudyDays: []int{1}}
		h.call("POST", "/study-plan", settings, 200)
		h.now = h.now.AddDate(0, 0, 8) // Tuesday, not a selected Monday.
		r := h.call("GET", "/study-plan", nil, 200)
		a := assignmentOn(t, r, r.Today, "")
		if !r.Plan.ReturnAfterPause || a.Kind != "REENTRY" || a.DurationMinutes > 30 {
			t.Fatalf("no reentry routing %+v", a)
		}
		if hasBank && (a.MaterialID == nil || a.Status != "PLANNED") {
			t.Fatal("genuine bank ignored")
		}
		if !hasBank && (a.MaterialID != nil || a.Status != "BLOCKED") {
			t.Fatal("empty-bank fabricated reentry")
		}
	}
}
