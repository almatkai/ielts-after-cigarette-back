package studyplan

import (
	"encoding/json"
	"sort"
	"time"
)

// Private bindings live with the user aggregate. Public cycleId is a navigation
// hint; it cannot replace this immutable correction/content context at start.
type learningAssignmentBinding struct {
	CycleID              string `json:"cycleId"`
	CorrectionGeneration int    `json:"correctionGeneration"`
	SourceKey            string `json:"sourceKey"`
	ContentHash          string `json:"contentHash"`
	FamilyID             string `json:"familyId"`
}

func transferDrillEligible(st *state, in inputs, c *learningCycle, u drillUnit, now time.Time) bool {
	d := u.Drill
	return c != nil && c.Cycle.Status == "awaiting-transfer" && c.Cycle.SkippedAt == nil && c.Cycle.Source.FamilyKnown && c.Cycle.Source.FamilyID != nil && c.Cycle.Correction != nil && c.Cycle.Correction.Verdict == "correct" && !c.Cycle.Correction.Assisted && d.MasteryEligible && d.FamilyID != nil && *d.FamilyID != *c.Cycle.Source.FamilyID && d.SkillID == c.Cycle.SkillID && !familySeen(st, d.FamilyID) && !knownSessionOrigin(st, d) && !sourceOriginMatches(c.Cycle, d) && !priorAttemptOrigin(in, d, now)
}
func assignmentBindingValid(st *state, a Assignment, u drillUnit, c *learningCycle) bool {
	if a.CycleID == nil || c == nil {
		return false
	}
	b, ok := st.Learning.AssignmentBindings[a.ID]
	return ok && b.CycleID == c.Cycle.ID && *a.CycleID == c.Cycle.ID && b.CorrectionGeneration == len(c.Revisions) && b.SourceKey == c.SourceKey && b.ContentHash == drillContentHash(u) && u.Drill.FamilyID != nil && b.FamilyID == *u.Drill.FamilyID
}
func hasOpenCycleSession(st *state, cycle string) bool {
	for _, s := range st.Learning.Sessions {
		if s.Session.Status != "in-progress" || s.Session.CycleID == nil || *s.Session.CycleID != cycle {
			continue
		}
		if s.Session.AssignmentID != nil {
			a, _ := learningAssignment(st, *s.Session.AssignmentID)
			if a != nil && (a.Status == "SKIPPED" || a.Status == "MISSED") {
				continue
			}
		}
		return true
	}
	return false
}
func mockScheduled(p Plan, day string) bool {
	for _, w := range p.Weeks {
		if w.MockOffer.Status == "SCHEDULED" && w.MockOffer.ScheduledDate != nil && *w.MockOffer.ScheduledDate == day {
			return true
		}
	}
	return false
}

// Keep one real, delayed check per cycle. The ordinary planner retains section
// maintenance; a check replaces same-section generic practice or uses free time.
func reconcileLearningAssignments(st *state, in inputs, today string, now time.Time) bool {
	before, _ := json.Marshal(struct {
		P Plan
		L learningState
	}{st.Plan, st.Learning})
	if st.Learning.AssignmentBindings == nil {
		st.Learning.AssignmentBindings = map[string]learningAssignmentBinding{}
	}
	pending := map[string]bool{}
	dayCheck := map[string]bool{}
	reserved := []Drill{}
	for di := range st.Plan.Days {
		d := &st.Plan.Days[di]
		kept := []Assignment{}
		for _, a := range d.Assignments {
			if d.Date < today || a.Status != "PLANNED" || a.SessionID != nil || a.AttemptID != nil {
				kept = append(kept, a)
				if a.CycleID != nil && a.Status != "SKIPPED" && a.Status != "MISSED" {
					dayCheck[d.Date] = true
				}
				continue
			}
			if a.DrillID == nil {
				kept = append(kept, a)
				continue
			}
			u := unitByID(in.Drills, *a.DrillID)
			// Old generic control assignments are removed without consuming the family.
			if a.CycleID == nil {
				if u == nil || u.Drill.MasteryEligible {
					continue
				}
				kept = append(kept, a)
				continue
			}
			c := cycleByID(st, *a.CycleID)
			valid := u != nil && assignmentBindingValid(st, a, *u, c) && transferDrillEligible(st, in, c, *u, now) && !pending[*a.CycleID] && !dayCheck[d.Date] && !hasOpenCycleSession(st, *a.CycleID) && selected(st.Settings, d.Date) && !d.IsRest && !mockScheduled(st.Plan, d.Date) && !deadlineReached(in.Goal, d.Date) && c.Cycle.NextCheckDate != nil && d.Date >= *c.Cycle.NextCheckDate && a.DurationMinutes <= d.BudgetMinutes
			if valid {
				pending[*a.CycleID] = true
				dayCheck[d.Date] = true
				reserved = append(reserved, u.Drill)
				kept = append(kept, a)
			} else {
				delete(st.Learning.AssignmentBindings, a.ID)
			}
		}
		d.Assignments = kept
		d.PlannedMinutes = plannedMinutes(*d)
	}
	units := append([]drillUnit{}, in.Drills...)
	sort.SliceStable(units, func(i, j int) bool { return units[i].Drill.ID < units[j].Drill.ID })
	for ci := range st.Learning.Cycles {
		c := &st.Learning.Cycles[ci]
		if c.Cycle.Status != "awaiting-transfer" || c.Cycle.SkippedAt != nil {
			continue
		}
		oldNext := c.Cycle.NextDrillID
		c.Cycle.NextDrillID = nil
		if pending[c.Cycle.ID] {
			for _, d := range st.Plan.Days {
				for _, a := range d.Assignments {
					if a.CycleID != nil && *a.CycleID == c.Cycle.ID && a.Status == "PLANNED" && a.SessionID == nil {
						c.Cycle.NextDrillID = a.DrillID
					}
				}
			}
		} else if !hasOpenCycleSession(st, c.Cycle.ID) {
			for _, u := range units {
				if !transferDrillEligible(st, in, c, u, now) || u.Drill.DurationMinutes > st.Settings.DailyMinutes {
					continue
				}
				available := true
				for _, d := range reserved {
					if sameOrigin(d, u.Drill) || sameKnown(d.FamilyID, u.Drill.FamilyID) {
						available = false
						break
					}
				}
				if !available {
					continue
				}
				// nextDrillId is availability, not a promise of a scheduled date.
				c.Cycle.NextDrillID = ptr(u.Drill.ID)
				for di := range st.Plan.Days {
					d := &st.Plan.Days[di]
					if dayCheck[d.Date] || d.Date <= today || c.Cycle.NextCheckDate == nil || d.Date < *c.Cycle.NextCheckDate || !selected(st.Settings, d.Date) || d.IsRest || d.ReadOnlyReason != nil || mockScheduled(st.Plan, d.Date) || deadlineReached(in.Goal, d.Date) {
						continue
					}
					replace := -1
					for ai, a := range d.Assignments {
						if a.Skill == c.Cycle.Section && a.CycleID == nil && a.Kind != "REVIEW" && a.Status == "PLANNED" && a.AttemptID == nil && a.SessionID == nil && d.BudgetMinutes-d.PlannedMinutes+a.DurationMinutes >= u.Drill.DurationMinutes {
							replace = ai
							break
						}
					}
					if replace < 0 && (len(d.Assignments) >= 3 || d.BudgetMinutes-d.PlannedMinutes < u.Drill.DurationMinutes) {
						continue
					}
					a := newAssignment("DRILL", u.Drill.Section, u.Drill.Title, "Отложенная проверка исходной правки: тот же микронавык, новое проверенное семейство; не прогноз балла IELTS", u.Drill.DurationMinutes)
					a.DrillID = ptr(u.Drill.ID)
					a.CycleID = ptr(c.Cycle.ID)
					a.Target = "/study-plan/learning/drills/" + u.Drill.ID
					if replace >= 0 {
						d.Assignments[replace] = a
					} else {
						d.Assignments = append(d.Assignments, a)
					}
					st.Learning.AssignmentBindings[a.ID] = learningAssignmentBinding{c.Cycle.ID, len(c.Revisions), c.SourceKey, drillContentHash(u), *u.Drill.FamilyID}
					d.PlannedMinutes = plannedMinutes(*d)
					pending[c.Cycle.ID] = true
					dayCheck[d.Date] = true
					reserved = append(reserved, u.Drill)
					break
				}
				break
			}
		}
		if !sameOptional(oldNext, c.Cycle.NextDrillID) {
			c.Cycle.Revision++
		}
	}
	after, _ := json.Marshal(struct {
		P Plan
		L learningState
	}{st.Plan, st.Learning})
	return string(before) != string(after)
}
