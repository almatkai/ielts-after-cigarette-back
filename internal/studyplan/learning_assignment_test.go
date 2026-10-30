package studyplan

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func boundLearningFixture(t *testing.T, budget int) (*harness, Cycle) {
	t.Helper()
	h := setup(t)
	h.create(budget)
	units := builtinDrills()
	for _, id := range []string{"iac-r-library", "iac-r-transport", "iac-r-volunteer"} {
		u := unitByID(units, id)
		approveFixture(t, h, *u, *u.Drill.FamilyID)
	}
	s := startDrill(t, h, "iac-r-library", "bound-source", nil)
	submitDrill(t, h, s, "bound-source-submit", "TRUE", false)
	var c Cycle
	for _, x := range learningGet(t, h).Cycles {
		if x.Source.SessionID != nil && *x.Source.SessionID == s.ID {
			c = x
		}
	}
	if c.ID == "" {
		t.Fatal("source cycle missing")
	}
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "bound-correction", "FALSE"), 200)
	return h, c
}
func pendingBound(t *testing.T, r Response, cycle string) (Assignment, string) {
	t.Helper()
	var found Assignment
	day := ""
	count := 0
	for _, d := range r.Plan.Days {
		for _, a := range d.Assignments {
			if a.CycleID != nil && *a.CycleID == cycle && a.Status == "PLANNED" && a.SessionID == nil {
				count++
				found = a
				day = d.Date
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one pending bound check, got %d", count)
	}
	return found, day
}
func startBound(t *testing.T, h *harness, a Assignment, rev int64, key string) Session {
	t.Helper()
	return learningCall[Session](t, h, "POST", "/drills/"+*a.DrillID+"/start", map[string]any{"expectedRevision": rev, "idempotencyKey": key, "assignmentId": a.ID}, 200)
}
func TestLearningPlanBoundChecksCompletePersistedCycleHTTP(t *testing.T) {
	for _, budget := range []int{30, 45, 60, 90} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			h, c := boundLearningFixture(t, budget)
			r := h.call("GET", "/study-plan", nil, 200)
			a, day := pendingBound(t, r, c.ID)
			if a.DrillID == nil || c.NextCheckDate == nil || day < *c.NextCheckDate || dayGap(r.Today, day) < 3 {
				t.Fatalf("check not really delayed %+v %s", a, day)
			}
			before := r.Plan.Revision
			r2 := h.call("GET", "/study-plan", nil, 200)
			a2, _ := pendingBound(t, r2, c.ID)
			if before != r2.Plan.Revision || a.ID != a2.ID {
				t.Fatal("GET reschedules check/revisions")
			}
			units := builtinDrills()
			maintained := map[string]int{}
			for _, d := range r.Plan.Days {
				if d.PlannedMinutes > d.BudgetMinutes {
					t.Fatalf("overbudget %+v", d)
				}
				for _, task := range d.Assignments {
					if task.DrillID != nil {
						u := unitByID(units, *task.DrillID)
						if u != nil && (*task.DrillID == "iac-r-library" || *task.DrillID == "iac-r-transport" || *task.DrillID == "iac-r-volunteer") && task.CycleID == nil {
							t.Fatal("control in generic pool")
						}
						if task.CycleID == nil {
							maintained[task.Skill]++
						}
					}
				}
			}
			if maintained["writing"] == 0 || maintained["speaking"] == 0 {
				t.Fatal("check displaced section maintenance")
			}
			// The generic learning list must not reveal reserved unseen task bodies.
			l := learningGet(t, h)
			for _, d := range l.Drills {
				if d.MasteryEligible && d.ID != "iac-r-library" && (d.Stimulus != "" || d.Task != "") {
					t.Fatalf("reserved content leaked before start %s", d.ID)
				}
			}
			if original := unitByID(builtinDrills(), *a.DrillID); original == nil || original.Drill.Stimulus == "" || original.Drill.Task == "" {
				t.Fatal("projection mutated actual exercise")
			}
			foreign := h.request("POST", "/study-plan/learning/drills/"+*a.DrillID+"/start", map[string]any{"expectedRevision": r.Plan.Revision, "idempotencyKey": "foreign", "assignmentId": a.ID}, h.otherToken)
			if foreign.Code != 404 {
				t.Fatalf("foreign assignment start %d", foreign.Code)
			}
			learningCall[Session](t, h, "POST", "/drills/"+*a.DrillID+"/start", map[string]any{"expectedRevision": r.Plan.Revision, "idempotencyKey": "early", "assignmentId": a.ID}, 409)
			h.now = mustTimestamp(day + "T12:00:00Z")
			r = h.call("GET", "/study-plan", nil, 200)
			a, day = pendingBound(t, r, c.ID)
			// Different click keys still resume exactly one assignment-owned session.
			var wg sync.WaitGroup
			responses := make(chan *httptest.ResponseRecorder, 6)
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					responses <- h.request("POST", "/study-plan/learning/drills/"+*a.DrillID+"/start", map[string]any{"expectedRevision": r.Plan.Revision, "idempotencyKey": fmt.Sprintf("bound-start-%d", i), "assignmentId": a.ID}, h.token)
				}(i)
			}
			wg.Wait()
			close(responses)
			var session Session
			for w := range responses {
				if w.Code != 200 {
					t.Fatalf("double-click start %d %s", w.Code, w.Body)
				}
				var got Session
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if session.ID != "" && session.ID != got.ID {
					t.Fatal("duplicate bound sessions")
				}
				session = got
			}
			if session.CycleID == nil || *session.CycleID != c.ID || session.AssignmentID == nil || *session.AssignmentID != a.ID || session.Drill.Stimulus == "" || session.Drill.Task == "" {
				t.Fatalf("start did not derive source context/full task %+v", session)
			}
			keyAnswer := "NOT GIVEN"
			if *a.DrillID == "iac-r-volunteer" {
				keyAnswer = "TRUE"
			}
			submitted := submitDrill(t, h, session, "bound-submit", keyAnswer, false)
			if !submitted.Independent {
				t.Fatalf("plan check failed: %s", submitted.IndependenceReason)
			}
			submitDrill(t, h, session, "bound-submit", keyAnswer, false)
			c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
			if len(c.Checks) != 1 || c.RemainingChecks != 1 {
				t.Fatalf("plan submission did not update cycle %+v", c)
			}
			r = h.call("GET", "/study-plan", nil, 200)
			next, nextDay := pendingBound(t, r, c.ID)
			if *next.DrillID == *a.DrillID || nextDay <= day {
				t.Fatal("old family reoffered or second check not future")
			}
			firstSaved, firstDay := learningAssignmentFromResponse(r, a.ID)
			if firstSaved == nil || firstDay != day || firstSaved.Status != "COMPLETED" || firstSaved.SessionID == nil || *firstSaved.SessionID != session.ID {
				t.Fatal("completed check overwritten")
			}
			h.now = mustTimestamp(nextDay + "T12:00:00Z")
			r = h.call("GET", "/study-plan", nil, 200)
			next, _ = pendingBound(t, r, c.ID)
			ss := startBound(t, h, next, r.Plan.Revision, "bound-second")
			answer := "TRUE"
			if *next.DrillID == "iac-r-transport" {
				answer = "NOT GIVEN"
			}
			submitDrill(t, h, ss, "bound-second-submit", answer, false)
			c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
			if c.Status != "demonstrated" || c.RemainingChecks != 0 || len(c.Checks) != 2 {
				t.Fatalf("bound end-to-end cycle incomplete %+v", c)
			}
			r = h.call("GET", "/study-plan", nil, 200)
			for _, d := range r.Plan.Days {
				for _, task := range d.Assignments {
					if task.CycleID != nil && *task.CycleID == c.ID && task.Status == "PLANNED" {
						t.Fatal("extra pending check after completion")
					}
				}
			}
		})
	}
}
func learningAssignmentFromResponse(r Response, id string) (*Assignment, string) {
	for di := range r.Plan.Days {
		for ai := range r.Plan.Days[di].Assignments {
			a := &r.Plan.Days[di].Assignments[ai]
			if a.ID == id {
				return a, r.Plan.Days[di].Date
			}
		}
	}
	return nil, ""
}

func TestLearningPlanBindingCorrectionChangesDeadlineAndSkippedChecks(t *testing.T) {
	h, c := boundLearningFixture(t, 30)
	r := h.call("GET", "/study-plan", nil, 200)
	old, day := pendingBound(t, r, c.ID)
	h.now = h.now.AddDate(0, 0, 1)
	c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "changed-context", "FALSE"), 200)
	r = h.call("GET", "/study-plan", nil, 200)
	updated, newDay := pendingBound(t, r, c.ID)
	if updated.ID == old.ID || newDay <= day {
		t.Fatal("correction edit preserved stale binding/delayed anchor")
	}
	h.now = mustTimestamp(newDay + "T12:00:00Z")
	r = h.call("GET", "/study-plan", nil, 200)
	updated, _ = pendingBound(t, r, c.ID)
	learningCall[Session](t, h, "POST", "/drills/"+*updated.DrillID+"/start", map[string]any{"expectedRevision": r.Plan.Revision, "idempotencyKey": "wrong-cycle", "assignmentId": updated.ID, "cycleId": "foreign-or-forged"}, 409)
	r = h.call("POST", "/study-plan/assignments/"+updated.ID+"/skip", map[string]any{"expectedRevision": r.Plan.Revision, "reason": "no_time"}, 200)
	next, nextDay := pendingBound(t, r, c.ID)
	if next.ID == updated.ID || nextDay <= r.Today {
		t.Fatal("skip counted as completed/reused skipped assignment")
	}
	c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
	if len(c.Checks) != 0 || c.RemainingChecks != 2 {
		t.Fatal("skip fabricated a check")
	}
	h.sql(`UPDATE user_profiles SET exam_date=$2 WHERE user_id=$1`, h.user, date(nextDay))
	r = h.call("GET", "/study-plan", nil, 200)
	for _, d := range r.Plan.Days {
		if d.Date >= nextDay {
			for _, a := range d.Assignments {
				if a.Status == "PLANNED" && a.CycleID != nil {
					t.Fatal("bound check past exam")
				}
			}
		}
	}
	_ = next
}

func TestLearningProgressLocalEventsAndCoherentProfileHTTP(t *testing.T) {
	h := setup(t)
	h.now = mustTimestamp("2026-10-24T11:30:00Z")
	h.sql(`UPDATE user_profiles SET timezone='Pacific/Kiritimati' WHERE user_id=$1`, h.user)
	h.create(30)
	source := startDrill(t, h, "iac-r-library", "local-source", nil)
	submitDrill(t, h, source, "local-source-submit", "TRUE", false)
	l := learningGet(t, h)
	var c Cycle
	for _, x := range l.Cycles {
		if x.Source.SessionID != nil && *x.Source.SessionID == source.ID {
			c = x
		}
	}
	h.now = h.now.Add(time.Hour)
	input := correctionInput(c, "local-correction", "FALSE")
	c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", input, 200)
	learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", input, 200)
	w := startDrill(t, h, "iac-w-grammar", "local-writing", nil)
	submitDrill(t, h, w, "local-writing-submit", "The number has increased; many students prefer online resources.", false)
	r := h.call("GET", "/study-plan", nil, 200)
	var found *Progress
	for i := range r.Progress {
		if r.Progress[i].Date == "2026-10-25" {
			found = &r.Progress[i]
		}
	}
	if found == nil || found.DrillsCompleted != 2 || found.CorrectionsSaved != 1 || found.IndependentChecksPassed != 0 || found.CompletedAttempts != 0 || found.ActiveMinutes != nil {
		t.Fatalf("local events missing/doubled %+v", found)
	}
	r2 := h.call("GET", "/study-plan", nil, 200)
	if r2.Progress[0] != r.Progress[0] {
		t.Fatal("reload changed event counts")
	}
	l = learningGet(t, h)
	for _, p := range l.Profiles {
		if p.SkillID == c.SkillID {
			if len(p.SourceIDs) != 1 || p.SourceIDs[0] != c.ID || p.Status != c.Status || p.IndependentFamilies != 0 || p.UpdatedAt == nil || *p.UpdatedAt != c.Correction.SavedAt {
				t.Fatalf("profile mixed cycles/activity %+v", p)
			}
		}
	}
	// The next real wrong source becomes the profile's sole active source; previous
	// cycle successes must not leak into its remaining checks or family count.
	h.now = h.now.Add(time.Hour)
	s := startDrill(t, h, "iac-r-transport", "new-profile-source", nil)
	submitDrill(t, h, s, "new-profile-submit", "TRUE", false)
	l = learningGet(t, h)
	for _, p := range l.Profiles {
		if p.SkillID == c.SkillID && (p.Status != "needs-practice" || p.RemainingChecks != 2 || p.IndependentFamilies != 0 || p.DelayedCheckPassed || len(p.SourceIDs) != 1 || p.SourceIDs[0] == c.ID) {
			t.Fatalf("incoherent latest source profile %+v", p)
		}
	}
}

func TestLearningCheckCapacityOnePerDayAndThreeRows(t *testing.T) {
	settings := allDays(45)
	st := state{Settings: settings, Plan: makePlan("2026-10-01", settings, 165), Learning: learningState{Zone: "UTC", Cycles: []learningCycle{}, Sessions: []learningSession{}}}
	in := inputs{Goal: Goal{}, Drills: []drillUnit{}}
	for di := range st.Plan.Days {
		d := &st.Plan.Days[di]
		d.BudgetMinutes = 45
		d.Assignments = []Assignment{newAssignment("PRACTICE", "writing", "Writing practice", "Fixture", 20), newAssignment("REVIEW", "writing", "Review", "Fixture", 10)}
		d.PlannedMinutes = 30
	}
	// A full three-row day has spare minutes but should not acquire a fourth row.
	st.Plan.Days[3].Assignments = append(st.Plan.Days[3].Assignments, newAssignment("PRACTICE", "speaking", "Speaking", "Fixture", 5))
	st.Plan.Days[3].PlannedMinutes = 35
	for i := 0; i < 6; i++ {
		family := fmt.Sprintf("control-family-%d", i)
		id := fmt.Sprintf("control-%d", i)
		in.Drills = append(in.Drills, drillUnit{Drill: Drill{ID: id, SkillID: "ms-r-tfng", Section: "reading", Title: id, DurationMinutes: 7, Modality: "objective", FamilyID: &family, Version: "fixture-1", Stimulus: fmt.Sprintf("Distinct isolated fixture paragraph %d", i), EditorialStatus: "reviewed", MasteryEligible: true}, Key: json.RawMessage(`{"value":"TRUE"}`)})
		correction := Correction{Text: "TRUE", SavedAt: "2026-10-01T12:00:00Z", Verdict: "correct"}
		sourceFamily := fmt.Sprintf("source-family-%d", i)
		st.Learning.Cycles = append(st.Learning.Cycles, learningCycle{Cycle: Cycle{ID: fmt.Sprintf("cycle-%d", i), Revision: 1, SkillID: "ms-r-tfng", Section: "reading", Status: "awaiting-transfer", Source: SourceEvidence{MaterialID: fmt.Sprintf("source-%d", i), FamilyID: &sourceFamily, FamilyKnown: true, Stimulus: fmt.Sprintf("Different original evidence %d", i)}, Correction: &correction, NextCheckDate: ptr("2026-10-04")}, SourceKey: fmt.Sprintf("source-key-%d", i), Revisions: []Correction{correction}})
	}
	if !reconcileLearningAssignments(&st, in, "2026-10-01", mustTimestamp("2026-10-01T12:00:00Z")) {
		t.Fatal("no checks assigned")
	}
	count := 0
	perCycle := map[string]int{}
	for _, d := range st.Plan.Days {
		bound := 0
		for _, a := range d.Assignments {
			if a.CycleID != nil {
				bound++
				perCycle[*a.CycleID]++
				count++
			}
		}
		if bound > 1 || len(d.Assignments) > 3 || d.PlannedMinutes > 45 {
			t.Fatalf("calendar check overload %+v", d)
		}
	}
	if count != 6 {
		t.Fatalf("checks lost instead of spread over dates: %d", count)
	}
	for _, n := range perCycle {
		if n != 1 {
			t.Fatal("multiple pending checks per cycle")
		}
	}
	if reconcileLearningAssignments(&st, in, "2026-10-01", mustTimestamp("2026-10-01T12:00:00Z")) {
		t.Fatal("pending checks changed on reload")
	}
}
func TestLearningWeakProfileDoesNotMixLatestStatusAndOldSuccesses(t *testing.T) {
	older := Cycle{ID: "old", Revision: 1, SkillID: "ms-r-tfng", Section: "reading", Status: "demonstrated", Source: SourceEvidence{OccurredAt: "2026-10-01T12:00:00Z"}, RemainingChecks: 0, Checks: []TransferCheck{{FamilyID: ptr("old-one"), Independent: true, Passed: true, Delayed: true}, {FamilyID: ptr("old-two"), Independent: true, Passed: true}}}
	newer := Cycle{ID: "new", Revision: 1, SkillID: "ms-r-tfng", Section: "reading", Status: "needs-practice", Source: SourceEvidence{OccurredAt: "2026-10-02T12:00:00Z"}, RemainingChecks: 2}
	st := state{Settings: allDays(30), Learning: learningState{Cycles: []learningCycle{{Cycle: older}, {Cycle: newer}}}}
	assert := func(wantStatus, wantTime string) {
		t.Helper()
		l := learningResponse(&st, inputs{Drills: []drillUnit{}}, "2026-10-03")
		for _, p := range l.Profiles {
			if p.SkillID == "ms-r-tfng" {
				if p.Status != wantStatus || p.UpdatedAt == nil || *p.UpdatedAt != wantTime || p.IndependentFamilies != 0 || p.DelayedCheckPassed || p.RemainingChecks != 2 || len(p.SourceIDs) != 1 || p.SourceIDs[0] != "new" {
					t.Fatalf("mixed-cycle profile %+v", p)
				}
				return
			}
		}
		t.Fatal("profile missing")
	}
	assert("needs-practice", "2026-10-02T12:00:00Z")
	st.Learning.Cycles[1].Cycle.Correction = &Correction{SavedAt: "2026-10-03T12:00:00Z"}
	st.Learning.Cycles[1].Cycle.Status = "correction-saved"
	assert("correction-saved", "2026-10-03T12:00:00Z")
}

// A wrong or assisted correction must not consume the scarce reviewed controls
// through the cycle's remedial-practice link, including a manually forged URL.
func TestLearningUnreadyCyclePreservesReviewedCheckFamiliesHTTP(t *testing.T) {
	for _, assisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("assisted-%t", assisted), func(t *testing.T) {
			h, c := boundLearningFixture(t, 45)
			answer := "TRUE"
			if assisted {
				answer = "FALSE"
			}
			input := correctionInput(c, "unready-correction", answer)
			input["assisted"] = assisted
			c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", input, 200)
			if c.Status == "awaiting-transfer" {
				t.Fatal("wrong/assisted correction became ready")
			}
			l := learningGet(t, h)
			if c.NextDrillID != nil {
				for _, d := range l.Drills {
					if d.ID == *c.NextDrillID && d.MasteryEligible {
						t.Fatal("remedial suggestion spends a reserved control")
					}
				}
			}
			r := h.call("GET", "/study-plan", nil, 200)
			learningCall[Session](t, h, "POST", "/drills/iac-r-transport/start", map[string]any{
				"expectedRevision": r.Plan.Revision, "idempotencyKey": "forged-cycle-start", "cycleId": c.ID,
			}, 409)
			for _, d := range learningGet(t, h).Drills {
				if d.ID == "iac-r-transport" && (d.Task != "" || d.Stimulus != "") {
					t.Fatal("rejected cycle start exposed or consumed control")
				}
			}
			c = learningCall[Cycle](t, h, "GET", "/cycles/"+c.ID, nil, 200)
			c = learningCall[Cycle](t, h, "POST", "/cycles/"+c.ID+"/correction", correctionInput(c, "own-correct-correction", "FALSE"), 200)
			a, _ := pendingBound(t, h.call("GET", "/study-plan", nil, 200), c.ID)
			if a.DrillID == nil || c.RemainingChecks != 2 {
				t.Fatal("control pool damaged by rejected start")
			}
		})
	}
}
