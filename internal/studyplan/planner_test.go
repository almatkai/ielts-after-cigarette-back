package studyplan

import (
	"strings"
	"testing"
	"time"
)

func allDays(minutes int) Settings {
	return Settings{DailyMinutes: minutes, StudyDays: []int{1, 2, 3, 4, 5, 6, 7}, WeeklyMockEnabled: true}
}
func TestPlannerBudgetsWholeMaterialsSpacingAndDeterminism(t *testing.T) {
	bank := []material{{ID: "reading", Skill: "reading", Title: "Single passage", Duration: 20, Passage: true}, {ID: "writing", Skill: "writing", Title: "Whole Writing", Duration: 60}, {ID: "listening", Skill: "listening", Title: "Whole Listening", Duration: 40}, {ID: "speaking", Skill: "speaking", Title: "Speaking", Duration: 15}}
	for _, minutes := range []int{30, 45, 60, 90} {
		t.Run(string(rune(minutes)), func(t *testing.T) {
			s := allDays(minutes)
			p := makePlan("2026-10-26", s, 165)
			q := makePlan("2026-10-26", s, 165)
			fillFuture(&p, s, p.StartDate, p.StartDate, bank, nil, false)
			fillFuture(&q, s, q.StartDate, q.StartDate, bank, nil, false)
			if len(p.Days) != 30 || p.EndDate != "2026-11-24" || len(p.Weeks) != 5 || p.Weeks[4].MockOffer.Status != "DISABLED" {
				t.Fatal("cycle shape")
			}
			last := map[string]string{}
			for i, d := range p.Days {
				if d.PlannedMinutes > d.BudgetMinutes || d.BudgetMinutes != minutes || len(d.Assignments) < 1 || len(d.Assignments) > 3 {
					t.Fatalf("bad day: %+v", d)
				}
				for j, a := range d.Assignments {
					b := q.Days[i].Assignments[j]
					if a.MaterialID == nil {
						if b.MaterialID != nil {
							t.Fatal("nondeterministic")
						}
						continue
					}
					if b.MaterialID == nil || *a.MaterialID != *b.MaterialID {
						t.Fatal("nondeterministic choice")
					}
					if a.Skill == "writing" && a.DurationMinutes != 60 {
						t.Fatal("truncated writing")
					}
					if a.Skill == "listening" && a.DurationMinutes != 40 {
						t.Fatal("truncated listening")
					}
					if old := last[*a.MaterialID]; old != "" {
						if dayGap(old, d.Date) < 7 || a.Kind == "CHECKPOINT" || !strings.Contains(a.Reason, "Повторная практика") {
							t.Fatalf("dishonest repeat: %+v", a)
						}
					}
					last[*a.MaterialID] = d.Date
				}
			}
			if len(last) == 0 {
				t.Fatal("small bank never assigned")
			}
		})
	}
}
func TestPlannerRestEmptyBankAndGeneralBlocked(t *testing.T) {
	s := Settings{DailyMinutes: 30, StudyDays: []int{1}}
	p := makePlan("2026-10-26", s, 165)
	fillFuture(&p, s, p.StartDate, p.StartDate, nil, nil, false)
	for _, d := range p.Days {
		if d.IsRest {
			if len(d.Assignments) != 0 || d.BudgetMinutes != 0 {
				t.Fatal("rest changed")
			}
		} else if len(d.Assignments) != 1 || d.Assignments[0].Status != "BLOCKED" || d.PlannedMinutes != 0 {
			t.Fatalf("fabricated material: %+v", d)
		}
	}
	for _, s := range []Settings{{DailyMinutes: 31, StudyDays: []int{1}}, {DailyMinutes: 30, StudyDays: []int{1, 1}}, {DailyMinutes: 30, StudyDays: []int{0}}, {DailyMinutes: 30}} {
		if validateSettings(s) == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
}
func TestReplanPreservesStartedSkippedAndBudget(t *testing.T) {
	s := allDays(60)
	p := makePlan("2026-10-26", s, 165)
	bank := []material{{ID: "r", Skill: "reading", Title: "reading", Duration: 50}}
	fillFuture(&p, s, p.StartDate, p.StartDate, bank, nil, false)
	p.Days[0].Assignments[0].Status = "COMPLETED"
	p.Days[0].Assignments[0].AttemptID = ptr("attempt")
	a := newAssignment("PRACTICE", "reading", "explicit skip", "skip", 0)
	a.Status = "SKIPPED"
	a.SkipReason = ptr("outside_app")
	p.Days[1].Assignments = append(p.Days[1].Assignments, a)
	fillFuture(&p, allDays(30), p.StartDate, p.StartDate, bank, nil, false)
	if p.Days[0].BudgetMinutes != 60 || p.Days[0].PlannedMinutes != 50 {
		t.Fatalf("history budget overwritten: %+v", p.Days[0])
	}
	found := false
	for _, a := range p.Days[1].Assignments {
		if a.ID == p.Days[1].Assignments[0].ID && a.Status == "SKIPPED" {
			found = true
		}
	}
	if !found {
		t.Fatal("skip erased")
	}
}
func TestLocalCalendarDSTAndReentry(t *testing.T) {
	zone, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	start := time.Date(2026, 10, 31, 23, 30, 0, 0, zone)
	end := start.Add(25 * time.Hour)
	if dayGap(localDate(start, zone), localDate(end, zone)) != 1 {
		t.Fatal("elapsed hours used for DST calendar")
	}
	for _, gap := range []int{6, 7, 14} {
		s := allDays(30)
		p := makePlan("2026-10-26", s, 165)
		today := addDays(p.StartDate, gap)
		bank := []material{{ID: "r", Skill: "reading", Title: "genuine short passage", Duration: 20}, {ID: "w", Skill: "writing", Title: "whole writing", Duration: 60}}
		fillFuture(&p, s, p.StartDate, p.StartDate, bank, nil, false)
		reconcileDay(&p, today)
		returning := dayGap(p.StartDate, today) >= 7
		fillFuture(&p, s, today, today, bank, nil, returning)
		d := p.Days[gap]
		if d.PlannedMinutes > 30 {
			t.Fatal("reentry exceeded budget")
		}
		if returning {
			for _, a := range d.Assignments {
				if a.Kind != "REENTRY" || a.DurationMinutes > 30 {
					t.Fatalf("bad reentry %+v", a)
				}
			}
		}
	}
}
func TestEvidenceLatestNotMaximumPendingRepeatStale(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	older := now.AddDate(0, 0, -35)
	recent := now.AddDate(0, 0, -1)
	results := []result{{ID: "pending", Skill: "reading", Status: "PROCESSING", MaterialID: "new", StartedAt: now}, {ID: "latest", Skill: "reading", Status: "SUBMITTED", MaterialID: "same", StartedAt: recent, SubmittedAt: &recent, Score: ptr(1), MaxScore: ptr(5), Band: ptr(3.0), Repeat: true}, {ID: "max", Skill: "reading", Status: "SUBMITTED", MaterialID: "same", StartedAt: older, SubmittedAt: &older, Band: ptr(9.0)}, {ID: "stale", Skill: "writing", Status: "SUBMITTED", MaterialID: "w", SubmittedAt: &older, Band: ptr(6.0)}}
	ev := evidence(results, now)
	r := ev[1]
	if r.Status != "AWAITING_RESULT" || r.PracticeBand != nil || r.Score == nil || *r.Score != 1 || r.AttemptID == nil || *r.AttemptID != "latest" || r.SampleCount != 1 || r.Band != nil {
		t.Fatalf("pending erased/overclaimed: %+v", r)
	}
	if ev[2].Status != "STALE" || ev[2].Source != "ai_practice" || ev[2].PracticeBand != nil {
		t.Fatalf("stale lost: %+v", ev[2])
	}
	if ev[0].PracticeBand != nil || ev[0].Status != "NO_EVIDENCE" {
		t.Fatal("invented score")
	}
	if len(recommendations(results)) != 0 {
		t.Fatal("fabricated recommendation")
	}
	results[1].Wrong = 2
	results[1].Reviewable = true
	results[3].Feedback = []byte(`{"tasks":[{"improvements":["Use precise linking words"]}]}`)
	results[3].Reviewable = true
	rec := recommendations(results)
	if len(rec) != 2 || rec[0].AttemptID == nil || rec[1].Title != "Use precise linking words" {
		t.Fatalf("not real feedback: %+v", rec)
	}
}
func TestMockValidationAndSupersession(t *testing.T) {
	st := state{Settings: allDays(45)}
	st.Plan = makePlan("2026-10-26", st.Settings, 172)
	fillFuture(&st.Plan, st.Settings, st.Plan.StartDate, st.Plan.StartDate, []material{{ID: "r", Skill: "reading", Title: "r", Duration: 20}}, nil, false)
	today := "2026-11-01"
	c := command{week: 1, mockAction: "schedule", scheduledDate: "2026-11-03", window: 171}
	if _, e := scheduleMock(&st, c, today); e != ErrValidation {
		t.Fatal("short window accepted")
	}
	c.window = 172
	if _, e := scheduleMock(&st, c, "2026-10-27"); e != ErrConflict {
		t.Fatal("early offer")
	}
	if changed, e := scheduleMock(&st, c, today); e != nil || !changed {
		t.Fatal(e)
	}
	for _, d := range st.Plan.Days {
		if d.Date == c.scheduledDate {
			if d.BudgetMinutes != 172 || d.PlannedMinutes != 0 {
				t.Fatal("long day")
			}
			for _, a := range d.Assignments {
				if a.Status != "SKIPPED" {
					t.Fatal("ordinary work not superseded")
				}
			}
		}
	}
	if changed, e := scheduleMock(&st, c, today); e != nil || changed {
		t.Fatal("schedule not idempotent")
	}
}
