package studyplan

import (
	"net/url"
	"time"
)

// Scope is a projection request, never a reconciliation clock.
type Scope struct {
	View          string  `json:"view"`
	Date          string  `json:"date"`
	StartDate     string  `json:"startDate"`
	EndDate       string  `json:"endDate"`
	Details       bool    `json:"details"`
	NextStudyDate *string `json:"nextStudyDate"`
}
type LearningSummary struct {
	PendingCorrections int  `json:"pendingCorrections"`
	ReadOnly           bool `json:"readOnly"`
}

func parseScope(q url.Values) (*Scope, error) {
	views, hasView := q["view"]
	if !hasView {
		if dates, ok := q["date"]; ok {
			if len(dates) != 1 {
				return nil, ErrValidation
			}
			d, err := time.Parse(time.DateOnly, dates[0])
			if err != nil || d.Format(time.DateOnly) != dates[0] {
				return nil, ErrValidation
			}
		}
		if _, ok := q["details"]; ok {
			return nil, ErrValidation
		}
		return nil, nil
	}
	if len(views) != 1 || (views[0] != "day" && views[0] != "week" && views[0] != "month") {
		return nil, ErrValidation
	}
	s := &Scope{View: views[0]}
	if dates, ok := q["date"]; ok {
		if len(dates) != 1 {
			return nil, ErrValidation
		}
		d, err := time.Parse(time.DateOnly, dates[0])
		if err != nil || d.Format(time.DateOnly) != dates[0] {
			return nil, ErrValidation
		}
		s.Date = dates[0]
	}
	if ds, ok := q["details"]; ok {
		if len(ds) != 1 || ds[0] != "1" {
			return nil, ErrValidation
		}
		s.Details = true
	}
	return s, nil
}
func projectPlan(p *Plan, request *Scope, today string) (*Plan, *Scope) {
	s := *request
	if s.Date == "" {
		s.Date = today
	}
	s.StartDate, s.EndDate = s.Date, s.Date
	if p == nil {
		return nil, &s
	}
	if s.View == "week" {
		for _, w := range p.Weeks {
			if s.Date >= w.StartDate && s.Date <= w.EndDate {
				s.StartDate, s.EndDate = w.StartDate, w.EndDate
			}
		}
		if today > p.EndDate && s.Date > p.EndDate && len(p.Weeks) > 0 {
			w := p.Weeks[len(p.Weeks)-1]
			s.StartDate, s.EndDate = w.StartDate, w.EndDate
		}
	}
	if s.View == "month" {
		date := s.Date
		if today > p.EndDate && date > p.EndDate {
			date = p.StartDate
		}
		start, _ := time.Parse(time.DateOnly, p.StartDate)
		selected, _ := time.Parse(time.DateOnly, date)
		gap := int((selected.Unix() - start.Unix()) / 86400)
		period := gap / 30
		if gap < 0 && gap%30 != 0 {
			period--
		}
		s.StartDate = addDays(p.StartDate, period*30)
		s.EndDate = addDays(s.StartDate, 29)
	}
	out := *p
	out.Days = []Day{}
	out.Weeks = []Week{}
	for _, d := range p.Days {
		if d.Date >= s.StartDate && d.Date <= s.EndDate {
			out.Days = append(out.Days, d)
		}
		if s.NextStudyDate == nil && d.Date > s.EndDate && !d.IsRest {
			s.NextStudyDate = ptr(d.Date)
		}
	}
	// Week metadata is small and includes offers from earlier weeks scheduled on
	// the selected day. It carries no assignments or day progress.
	for _, w := range p.Weeks {
		if s.View == "day" || (w.EndDate >= s.StartDate && w.StartDate <= s.EndDate) {
			out.Weeks = append(out.Weeks, w)
		}
	}
	if !s.Details {
		out.Changes = []Change{}
	}
	return &out, &s
}
func compactLearning(st *state, goal Goal, today string) *LearningSummary {
	out := &LearningSummary{ReadOnly: deadlineReached(goal, today)}
	for _, saved := range st.Learning.Cycles {
		c := saved.Cycle
		if c.SkippedAt == nil && (c.Status == "needs-practice" || (c.Correction != nil && c.Correction.Verdict == "incorrect")) {
			out.PendingCorrections++
		}
	}
	return out
}
