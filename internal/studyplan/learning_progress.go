package studyplan

import "time"

// Counts saved events, not elapsed learning time or predicted score growth.
// Every real correction revision counts once; idempotent replays append nothing.
func learningProgress(rows []Progress, ls learningState, zone *time.Location) []Progress {
	byDate := map[string]*Progress{}
	for i := range rows {
		byDate[rows[i].Date] = &rows[i]
	}
	eventDay := func(stamp string) *Progress {
		t, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			return nil
		}
		return byDate[localDate(t, zone)]
	}
	submitted := map[string]bool{}
	for _, saved := range ls.Sessions {
		s := saved.Session
		if s.Status != "submitted" || s.SubmittedAt == nil || submitted[s.ID] {
			continue
		}
		submitted[s.ID] = true
		if p := eventDay(*s.SubmittedAt); p != nil {
			p.DrillsCompleted++
			if s.Independent && s.Verdict != nil && *s.Verdict == "correct" {
				p.IndependentChecksPassed++
			}
		}
	}
	for _, c := range ls.Cycles {
		if len(c.Revisions) > 0 {
			for _, r := range c.Revisions {
				if p := eventDay(r.SavedAt); p != nil {
					p.CorrectionsSaved++
				}
			}
		} else if c.Cycle.Correction != nil {
			if p := eventDay(c.Cycle.Correction.SavedAt); p != nil {
				p.CorrectionsSaved++
			}
		}
	}
	return rows
}
func cycleUpdatedAt(c Cycle) string {
	updated := c.Source.OccurredAt
	if c.Correction != nil && c.Correction.SavedAt > updated {
		updated = c.Correction.SavedAt
	}
	for _, check := range c.Checks {
		if check.OccurredAt > updated {
			updated = check.OccurredAt
		}
	}
	return updated
}
