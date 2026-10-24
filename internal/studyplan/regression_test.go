package studyplan

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSmallBankPracticeFirstReviewsBoundedWeeklyCheckpoint(t *testing.T) {
	settings := Settings{DailyMinutes: 30, StudyDays: []int{1, 2, 3, 4, 5, 6}}
	p := makePlan("2026-10-26", settings, 165) // Monday; Sunday is rest.
	bank := []material{
		{ID: "s", Skill: "speaking", Title: "New speaking", Duration: 11},
		{ID: "r1", Skill: "reading", Title: "New reading 1", Duration: 20},
		{ID: "r2", Skill: "reading", Title: "New reading 2", Duration: 20},
		{ID: "r3", Skill: "reading", Title: "New reading 3", Duration: 20},
		{ID: "r4", Skill: "reading", Title: "New reading 4", Duration: 20},
	}
	old := date("2026-10-01")
	results := []result{}
	for i := 0; i < 7; i++ {
		results = append(results, result{ID: fmt.Sprint(i), Skill: "reading", MaterialID: "old", Title: "Historical worksheet", Status: "SUBMITTED", SubmittedAt: &old, Wrong: 1, Reviewable: true})
	}
	fillFuture(&p, settings, p.StartDate, p.StartDate, bank, results, false)
	assertSmallBankPlan(t, p)
	first := p.Days[0].Assignments[0]
	if first.Skill != "speaking" || first.MaterialID == nil || first.Kind != "PRACTICE" {
		t.Fatalf("missing executable component lost priority: %+v", first)
	}
	for _, d := range p.Days {
		for _, a := range d.Assignments {
			if a.Kind == "REVIEW" && (!strings.Contains(a.Title, "Historical worksheet") || !strings.Contains(a.Title, "2026-10-01")) {
				t.Fatalf("review is not identifiable: %+v", a)
			}
		}
	}
}

// Shared by the pure planner and real-Postgres HTTP regression.
func assertSmallBankPlan(t *testing.T, p Plan) {
	t.Helper()
	practiceDays := 0
	last := map[string]string{}
	goals := map[string]bool{}
	for _, w := range p.Weeks {
		goals[w.Goal] = true
		if !strings.Contains(w.Goal, "Данных о неверных ответах") || strings.Contains(w.Goal, "Reading — 7") || !strings.Contains(w.Goal, "Writing") || strings.Contains(w.Goal, "TFNG") {
			t.Fatalf("weekly goal lacks honest evidence: %s", w.Goal)
		}
	}
	if len(goals) != 5 {
		t.Fatalf("weekly phases identical: %+v", p.Weeks)
	}
	for _, d := range p.Days {
		if d.PlannedMinutes > d.BudgetMinutes {
			t.Fatalf("budget exceeded: %+v", d)
		}
		reviews, practice := 0, 0
		for _, a := range d.Assignments {
			if a.Kind == "REVIEW" {
				reviews++
				if a.DurationMinutes != 10 {
					t.Fatalf("unexpected review allocation: %+v", a)
				}
			}
			if a.MaterialID != nil {
				practice++
				if previous := last[*a.MaterialID]; previous != "" && dayGap(previous, d.Date) < 7 {
					t.Fatalf("repeat too close: %s -> %s", previous, d.Date)
				}
				last[*a.MaterialID] = d.Date
			}
			if a.Status == "BLOCKED" && (a.Target != "/practice" || !strings.Contains(a.Reason, "Банк материалов ограничен") || !strings.Contains(a.Reason, "2026-")) {
				t.Fatalf("blocked action/reason misleading: %+v", a)
			}
			if a.Kind == "CHECKPOINT" && d.Date != lastStudyDay(p, Settings{StudyDays: []int{1, 2, 3, 4, 5, 6}}, d.Week) {
				t.Fatalf("checkpoint on wrong day: %+v", d)
			}
		}
		if reviews > 1 || practice > 1 {
			t.Fatalf("bank or budget consumed greedily: %+v", d)
		}
		if d.Week == 1 && practice > 0 {
			practiceDays++
		}
	}
	if practiceDays != 5 || p.Days[6].IsRest != true || len(p.Days[6].Assignments) != 0 {
		t.Fatalf("bank not spread over first week: %+v", p.Days[:7])
	}
	checkpoint := false
	for _, a := range p.Days[5].Assignments {
		checkpoint = checkpoint || a.Kind == "CHECKPOINT"
	}
	if !checkpoint {
		t.Fatal("Saturday must contain first-week new-material checkpoint")
	}
}

func TestPracticeAdaptsToRecentWrongEvidenceWithoutInventedTags(t *testing.T) {
	s := allDays(30)
	bank := []material{{ID: "r", Skill: "reading", Duration: 20}, {ID: "l", Skill: "listening", Duration: 20}}
	old := date("2026-10-25")
	for _, priority := range []string{"reading", "listening"} {
		p := makePlan("2026-10-26", s, 165)
		results := []result{{Skill: "reading", MaterialID: "old-r", Status: "SUBMITTED", SubmittedAt: &old}, {Skill: "listening", MaterialID: "old-l", Status: "SUBMITTED", SubmittedAt: &old}}
		for i := range results {
			if results[i].Skill == priority {
				results[i].Wrong = 4
			}
		}
		fillFuture(&p, s, p.StartDate, p.StartDate, bank, results, false)
		a := p.Days[0].Assignments[0]
		if a.Skill != priority || !strings.Contains(a.Reason, "неверных ответов — 4") || !strings.Contains(a.Reason, "Последняя работа за 14 дней") {
			t.Fatalf("actual error adaptation absent: %+v", a)
		}
	}
}

func TestUnviewedReviewCanBeReassignedAndCompletedReviewCannot(t *testing.T) {
	for _, status := range []string{"MISSED", "SKIPPED", "COMPLETED"} {
		t.Run(status, func(t *testing.T) {
			s := allDays(30)
			p := makePlan("2026-10-26", s, 165)
			old := date("2026-10-01")
			results := []result{{ID: "source", Skill: "reading", Status: "SUBMITTED", SubmittedAt: &old, Wrong: 1, Reviewable: true}}
			p.Days[0].Assignments = []Assignment{{Kind: "REVIEW", Status: status, SourceAttemptID: ptr("source")}}
			today := addDays(p.StartDate, 8)
			fillFuture(&p, s, today, today, nil, results, true)
			found := false
			for _, a := range p.Days[8].Assignments {
				found = found || (a.Kind == "REVIEW" && a.SourceAttemptID != nil && *a.SourceAttemptID == "source")
			}
			if found != (status != "COMPLETED") {
				t.Fatalf("unviewed/acknowledged review confused: %s %+v", status, p.Days[8])
			}
		})
	}
}

func TestNoLegacyOrAIBandEscapesEvidence(t *testing.T) {
	now := date("2026-10-26")
	for _, tc := range []struct {
		name, skill string
		score, max  int
		repeat      bool
	}{
		{"partial", "reading", 8, 10, false}, {"passage", "reading", 1, 1, false},
		{"repeat", "listening", 38, 40, true}, {"zero", "reading", 0, 40, false},
		{"full-unvalidated", "listening", 38, 40, false},
		{"writing-ai", "writing", 0, 0, false}, {"speaking-ai", "speaking", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := result{Skill: tc.skill, Status: "SUBMITTED", SubmittedAt: &now, Score: ptr(tc.score), MaxScore: ptr(tc.max), Band: ptr(8.5), Repeat: tc.repeat}
			for _, e := range evidence([]result{r}, now) {
				if e.Band != nil || e.PracticeBand != nil {
					t.Fatalf("unvalidated band escaped: %+v", e)
				}
				if e.Skill == tc.skill {
					if tc.skill == "writing" || tc.skill == "speaking" {
						if e.Score != nil || e.MaxScore != nil || e.Source != "ai_practice" {
							t.Fatalf("numeric AI result escaped: %+v", e)
						}
					} else if e.Score == nil || *e.Score != tc.score || e.MaxScore == nil || *e.MaxScore != tc.max {
						t.Fatalf("raw evidence lost: %+v", e)
					}
				}
			}
		})
	}
}

func TestHandlerFiveMaterialBankAndSevenHistoricalWrongAttempts(t *testing.T) {
	h := setup(t)
	withdrawBuiltinPractice(h)
	for i := 0; i < 4; i++ {
		h.seedReading(20)
	}
	speaking, version := uuid.New(), uuid.New()
	h.sql(`INSERT INTO speaking_materials(id,slug) VALUES($1,$2)`, speaking, "speaking-"+speaking.String())
	h.sql(`INSERT INTO speaking_material_versions(id,material_id,version_number,exam_type,difficulty,title,parts,created_by) VALUES($1,$2,1,'academic','intermediate','Real short speaking','[{"preparationSeconds":60,"responseSeconds":600}]',$3)`, version, speaking, h.user)
	h.sql(`UPDATE speaking_materials SET status='PUBLISHED',published_version_id=$2 WHERE id=$1`, speaking, version)
	oldMaterial, oldVersion, question := h.seedReading(20)
	h.sql(`UPDATE reading_materials SET status='ARCHIVED' WHERE id=$1`, oldMaterial)
	h.sql(`UPDATE reading_material_versions SET title='Historical errors worksheet' WHERE id=$1`, oldVersion)
	old := h.now.AddDate(0, 0, -20)
	for i := 0; i < 7; i++ {
		id := uuid.New()
		at := old.Add(time.Duration(i) * time.Hour)
		h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,band,started_at,submitted_at) VALUES($1,$2,'reading',$3,$4,'SUBMITTED',0,1,0,$5,$5)`, id, h.user, oldMaterial, oldVersion, at)
		h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer,is_correct,points_awarded) VALUES($1,$2,'{"value":"FALSE"}',false,0)`, id, question)
	}
	r := h.call("POST", "/study-plan", Settings{DailyMinutes: 30, StudyDays: []int{1, 2, 3, 4, 5, 6}}, 200)
	assertSmallBankPlan(t, *r.Plan)
	if r.Plan.Days[0].Assignments[0].Skill != "speaking" {
		t.Fatal("missing executable speaking not prioritized")
	}
	review := assignmentOn(t, r, r.Today, "REVIEW")
	if !strings.Contains(review.Title, "Historical errors worksheet") || !strings.Contains(review.Title, old.Format(time.DateOnly)) {
		t.Fatalf("historical material title/date not loaded: %+v", review)
	}
	if r.Evidence[1].PracticeBand != nil || r.Evidence[1].Band != nil || r.Evidence[1].Score == nil || *r.Evidence[1].Score != 0 || r.OverallBand != nil {
		t.Fatalf("raw/legacy score confused: %+v", r.Evidence[1])
	}
}

func TestHandlerMissedReviewReassignedOnReentryWithoutCountingViewed(t *testing.T) {
	h := setup(t)
	withdrawBuiltinPractice(h)
	m, v, q := h.seedReading(60) // No executable short practice: review is suitable reentry.
	id := uuid.New()
	old := h.now.AddDate(0, 0, -3)
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,started_at,submitted_at) VALUES($1,$2,'reading',$3,$4,'SUBMITTED',0,1,$5,$5)`, id, h.user, m, v, old)
	h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer,is_correct,points_awarded) VALUES($1,$2,'{"value":"FALSE"}',false,0)`, id, q)
	r := h.create(30)
	original := assignmentOn(t, r, r.Today, "REVIEW")
	h.now = h.now.AddDate(0, 0, 8)
	r = h.call("GET", "/study-plan", nil, 200)
	reassigned := assignmentOn(t, r, r.Today, "REVIEW")
	if !r.Plan.ReturnAfterPause || reassigned.ID == original.ID || reassigned.SourceAttemptID == nil || *reassigned.SourceAttemptID != id.String() || r.Plan.Days[0].Assignments[0].Status != "MISSED" {
		t.Fatalf("missed review lost or falsely acknowledged: %+v", r.Plan)
	}
	for _, day := range r.Progress {
		if day.ReviewedAssignments != 0 {
			t.Fatalf("unviewed review counted: %+v", day)
		}
	}
	r = h.call("POST", "/study-plan/assignments/"+reassigned.ID+"/review", mutationInput{ExpectedRevision: r.Plan.Revision}, 200)
	if r.Progress[8].ReviewedAssignments != 1 || r.Progress[0].ReviewedAssignments != 0 {
		t.Fatal("explicit acknowledgement not counted on actual day")
	}
}

func TestHandlerDayTransitionWithoutMissedWorkDoesNotAnnounceMisses(t *testing.T) {
	h := setup(t)
	h.call("POST", "/study-plan", Settings{DailyMinutes: 30, StudyDays: []int{3}}, 200) // Mon/Tue rest.
	h.now = h.now.AddDate(0, 0, 1)
	r := h.call("GET", "/study-plan", nil, 200)
	for _, c := range r.Plan.Changes {
		if c.Type == "DAY_RECONCILED" {
			t.Fatalf("empty rest day announced missed work: %+v", c)
		}
	}
	again := h.call("GET", "/study-plan", nil, 200)
	if again.Plan.Revision != r.Plan.Revision {
		t.Fatal("same-day read mutated aggregate")
	}
	h.now = h.now.AddDate(0, 0, 2) // Wednesday's actual blocked assignment becomes missed.
	r = h.call("GET", "/study-plan", nil, 200)
	count := 0
	for _, c := range r.Plan.Changes {
		if c.Type == "DAY_RECONCILED" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("actual transition must be announced exactly once: %+v", r.Plan.Changes)
	}
}

func TestHandlerUnvalidatedRawAndAIEvaluationsHaveNoBands(t *testing.T) {
	h := setup(t)
	old, recent := h.now.AddDate(0, 0, -3), h.now.AddDate(0, 0, -1)
	readingMaterial, readingVersion, _ := h.seedReading(20)
	h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,band,started_at,submitted_at) VALUES($1,$2,'reading',$3,$4,'SUBMITTED',8,10,6.5,$5,$5)`, uuid.New(), h.user, readingMaterial, readingVersion, recent)
	listeningMaterial := uuid.New()
	for _, at := range []time.Time{old, recent} {
		h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,band,started_at,submitted_at) VALUES($1,$2,'listening',$3,$4,'SUBMITTED',38,40,8.5,$5,$5)`, uuid.New(), h.user, listeningMaterial, uuid.New(), at)
	}
	for _, skill := range []string{"writing", "speaking"} {
		id := uuid.New()
		h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,band,started_at,submitted_at) VALUES($1,$2,$3,$4,$5,'SUBMITTED',85,90,8.5,$6,$6)`, id, h.user, skill, uuid.New(), uuid.New(), recent)
		if skill == "writing" {
			h.sql(`INSERT INTO writing_evaluations(attempt_id,model,overall_band,task_response_band,coherence_band,lexical_resource_band,grammar_band,feedback) VALUES($1,'unvalidated-test',8.5,8.5,8.5,8.5,8.5,'{"improvements":["Develop the argument"]}')`, id)
		} else {
			h.sql(`INSERT INTO speaking_evaluations(attempt_id,model,overall_band,fluency_band,lexical_resource_band,grammar_band,pronunciation_band,feedback) VALUES($1,'unvalidated-test',8.5,8.5,8.5,8.5,8.5,'{"improvements":["Extend the answer"]}')`, id)
		}
	}
	r := h.call("GET", "/study-plan", nil, 200)
	if r.OverallBand != nil {
		t.Fatal("unvalidated overall exposed")
	}
	for _, e := range r.Evidence {
		if e.PracticeBand != nil || e.Band != nil {
			t.Fatalf("stored legacy/AI band exposed: %+v", e)
		}
		if e.Skill == "writing" || e.Skill == "speaking" {
			if e.Source != "ai_practice" || e.Score != nil || e.MaxScore != nil || e.AssessedAt == nil {
				t.Fatalf("AI raw numeric result/date handling incorrect: %+v", e)
			}
		} else if e.Score == nil || e.MaxScore == nil {
			t.Fatalf("raw L/R suppressed: %+v", e)
		}
		if e.Skill == "reading" && (*e.Score != 8 || *e.MaxScore != 10) {
			t.Fatalf("partial raw rescaled: %+v", e)
		}
		if e.Skill == "listening" && (!strings.Contains(strings.Join(e.Limitations, " "), "Повторная практика") || e.SampleCount != 1) {
			t.Fatalf("repeat evidence overclaimed: %+v", e)
		}
	}
}

func TestUnsuitableSkipCooldownAndReviewExclusionDifferFromNoTimeAndMissed(t *testing.T) {
	for _, tc := range []struct{ status, reason string }{{"SKIPPED", "unsuitable"}, {"SKIPPED", "no_time"}, {"MISSED", ""}} {
		for _, kind := range []string{"PRACTICE", "REVIEW"} {
			t.Run(tc.status+"/"+tc.reason+"/"+kind, func(t *testing.T) {
				s := allDays(30)
				p := makePlan("2026-10-26", s, 165)
				a := newAssignment(kind, "reading", "Skipped work", "", 20)
				a.Status = tc.status
				a.SkipReason = ptr(tc.reason)
				bank, results := []material{}, []result{}
				if kind == "PRACTICE" {
					a.MaterialID = ptr("material")
					bank = append(bank, material{ID: "material", Skill: "reading", Duration: 20})
				} else {
					a.SourceAttemptID = ptr("source")
					old := date("2026-10-01")
					results = append(results, result{ID: "source", Skill: "reading", MaterialID: "old-material", Status: "SUBMITTED", SubmittedAt: &old, Wrong: 1, Reviewable: true})
				}
				p.Days[0].Assignments = []Assignment{a}
				fillFuture(&p, s, addDays(p.StartDate, 1), p.StartDate, bank, results, false)
				first := -1
				for i, d := range p.Days[1:] {
					for _, candidate := range d.Assignments {
						if (kind == "PRACTICE" && candidate.MaterialID != nil) || (kind == "REVIEW" && candidate.SourceAttemptID != nil) {
							first = i + 1
							break
						}
					}
					if first >= 0 {
						break
					}
				}
				if tc.reason == "unsuitable" {
					if (kind == "PRACTICE" && first != 7) || (kind == "REVIEW" && first != -1) {
						t.Fatalf("unsuitable rejection ignored: first reassignment day index %d", first)
					}
				} else if first != 1 {
					t.Fatalf("no-time/missed work incorrectly suppressed: first reassignment day index %d", first)
				}
				for _, day := range progress(&p, results, time.UTC) {
					if day.ReviewedAssignments != 0 || day.CompletedAttempts != 0 {
						t.Fatalf("skip counted as viewing/completion: %+v", day)
					}
				}
			})
		}
	}
}

func TestHandlerUnsuitableAndNoTimeSkipsHaveDistinctReofferPolicyWithoutViews(t *testing.T) {
	for _, reason := range []string{"unsuitable", "no_time"} {
		t.Run(reason, func(t *testing.T) {
			h := setup(t)
			withdrawBuiltinPractice(h)
			material, _, _ := h.seedReading(20)
			oldMaterial, oldVersion, q := h.seedReading(20)
			h.sql(`UPDATE reading_materials SET status='ARCHIVED' WHERE id=$1`, oldMaterial)
			source := uuid.New()
			old := h.now.AddDate(0, 0, -3)
			h.sql(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id,status,score,max_score,started_at,submitted_at) VALUES($1,$2,'reading',$3,$4,'SUBMITTED',0,1,$5,$5)`, source, h.user, oldMaterial, oldVersion, old)
			h.sql(`INSERT INTO attempt_answers(attempt_id,question_id,answer,is_correct,points_awarded) VALUES($1,$2,'{"value":"FALSE"}',false,0)`, source, q)
			r := h.create(30)
			practice := assignmentOn(t, r, r.Today, "PRACTICE")
			review := assignmentOn(t, r, r.Today, "REVIEW")
			for _, a := range []Assignment{practice, review} {
				r = h.call("POST", "/study-plan/assignments/"+a.ID+"/skip", map[string]any{"expectedRevision": r.Plan.Revision, "reason": reason}, 200)
			}
			r = h.call("POST", "/study-plan/replan", mutationInput{ExpectedRevision: r.Plan.Revision}, 200)
			h.now = h.now.AddDate(0, 0, 1)
			r = h.call("GET", "/study-plan", nil, 200)
			firstPractice, firstReview := "", ""
			for _, d := range r.Plan.Days[1:] {
				for _, a := range d.Assignments {
					if firstPractice == "" && a.MaterialID != nil && *a.MaterialID == material.String() {
						firstPractice = d.Date
					}
					if firstReview == "" && a.SourceAttemptID != nil && *a.SourceAttemptID == source.String() {
						firstReview = d.Date
					}
				}
			}
			if reason == "unsuitable" {
				if firstPractice != addDays(r.Plan.StartDate, 7) || firstReview != "" {
					t.Fatalf("unsuitable work reoffered too early: practice=%s review=%s", firstPractice, firstReview)
				}
			} else if firstPractice != r.Today || firstReview != r.Today {
				t.Fatalf("no-time work incorrectly suppressed: practice=%s review=%s today=%s", firstPractice, firstReview, r.Today)
			}
			for _, a := range r.Plan.Days[0].Assignments {
				if a.Status != "SKIPPED" || a.SkipReason == nil || *a.SkipReason != reason || a.CompletedAt != nil {
					t.Fatalf("original skip history rewritten: %+v", a)
				}
			}
			for _, day := range r.Progress {
				if day.ReviewedAssignments != 0 || day.CompletedAttempts != 0 {
					t.Fatalf("skip counted as viewed or completed: %+v", day)
				}
			}
			if r.Progress[0].SkippedAssignments != 2 {
				t.Fatalf("explicit skips lost: %+v", r.Progress[0])
			}
		})
	}
}

func TestHandlerContinuingPauseAnnouncesReentryOnlyOnTransition(t *testing.T) {
	h := setup(t)
	h.seedReading(20)
	h.create(30)
	h.now = h.now.AddDate(0, 0, 7)
	for day := 0; day < 3; day++ {
		r := h.call("GET", "/study-plan", nil, 200)
		reentries, missed := 0, 0
		for _, c := range r.Plan.Changes {
			if c.Type == "REENTRY" {
				reentries++
			}
			if c.Type == "DAY_RECONCILED" {
				missed++
			}
		}
		if !r.Plan.ReturnAfterPause || reentries != 1 || missed != day+1 {
			t.Fatalf("continuing pause repeated notice or lost actual misses: day=%d reentries=%d missed=%d", day, reentries, missed)
		}
		h.now = h.now.AddDate(0, 0, 1)
	}
}
