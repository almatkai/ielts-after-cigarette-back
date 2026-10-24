package studyplan

import "encoding/json"

func reconcileDrillSessions(st *state) {
	for _, ss := range st.Learning.Sessions {
		s := ss.Session
		if s.AssignmentID == nil {
			continue
		}
		a, _ := learningAssignment(st, *s.AssignmentID)
		if a == nil || a.Status == "SKIPPED" {
			continue
		}
		a.SessionID = ptr(s.ID)
		if s.Status == "submitted" {
			a.Status = "COMPLETED"
			a.CompletedAt = s.SubmittedAt
		} else {
			a.Status = "IN_PROGRESS"
		}
	}
}
func learningResults(results []result, ls learningState) []result {
	// Resolved source mistakes no longer exert weakness priority. Source records and
	// answers remain intact; this is only a planner projection of recent evidence.
	out := append([]result{}, results...)
	resolved := map[string]int{}
	for _, c := range ls.Cycles {
		if c.Cycle.Correction != nil && c.Cycle.Correction.Verdict == "correct" && !c.Cycle.Correction.Assisted {
			resolved[c.Cycle.Source.AttemptID]++
		}
	}
	for i := range out {
		out[i].Questions = append([]questionEvidence{}, out[i].Questions...)
		for qi := range out[i].Questions {
			for _, c := range ls.Cycles {
				if c.Cycle.Source.AttemptID == out[i].ID && c.Cycle.Source.QuestionID != nil && *c.Cycle.Source.QuestionID == out[i].Questions[qi].ID && c.Cycle.Correction != nil && c.Cycle.Correction.Verdict == "correct" && !c.Cycle.Correction.Assisted {
					out[i].Questions[qi].Correct = true
				}
			}
		}
		out[i].Wrong -= resolved[out[i].ID]
		if out[i].Wrong < 0 {
			out[i].Wrong = 0
		}
	}
	return out
}
func applyExamDeadline(p *Plan, goal Goal, today string) bool {
	before, _ := json.Marshal(p)
	p.ExamDate = goal.ExamDate
	for di := range p.Days {
		d := &p.Days[di]
		if goal.ExamDate == nil || d.Date < *goal.ExamDate {
			if d.ReadOnlyReason != nil {
				d.ReadOnlyReason = nil
			}
			continue
		}
		d.ReadOnlyReason = ptr("Дата экзамена достигнута: новые задания не назначаются. Сохранённые работы доступны для просмотра.")
		d.IsRest = true
		d.BudgetMinutes = 0
		kept := []Assignment{}
		for _, a := range d.Assignments {
			if a.AttemptID != nil || a.SessionID != nil || terminal(a.Status) {
				kept = append(kept, a)
			}
		}
		d.Assignments = kept
		d.PlannedMinutes = plannedMinutes(*d)
	}
	for wi := range p.Weeks {
		w := &p.Weeks[wi]
		if goal.ExamDate != nil && (w.StartDate >= *goal.ExamDate || (w.MockOffer.ScheduledDate != nil && *w.MockOffer.ScheduledDate >= *goal.ExamDate)) {
			w.MockOffer.Status = "DISABLED"
			w.MockOffer.ScheduledDate = nil
			w.MockOffer.WindowMinutes = nil
		}
	}
	after, _ := json.Marshal(p)
	return string(before) != string(after)
}
