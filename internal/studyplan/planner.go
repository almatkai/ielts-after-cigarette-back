package studyplan

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func localDate(t time.Time, zone *time.Location) string { return t.In(zone).Format(time.DateOnly) }
func date(s string) time.Time                           { t, _ := time.Parse(time.DateOnly, s); return t }
func addDays(s string, days int) string                 { return date(s).AddDate(0, 0, days).Format(time.DateOnly) }

// Calendar arithmetic deliberately ignores elapsed hours: DST days are not 24h.
func dayGap(a, b string) int { return int(date(b).Sub(date(a)).Hours() / 24) }
func selected(s Settings, day string) bool {
	weekday := int(date(day).Weekday())
	if weekday == 0 {
		weekday = 7
	}
	for _, d := range s.StudyDays {
		if d == weekday {
			return true
		}
	}
	return false
}
func terminal(status string) bool {
	return status == "COMPLETED" || status == "SKIPPED" || status == "MISSED"
}
func newAssignment(kind, skill, title, reason string, minutes int) Assignment {
	return Assignment{ID: uuid.NewString(), Kind: kind, Skill: skill, Title: title, Reason: reason, DurationMinutes: minutes, Status: "PLANNED"}
}
func plannedMinutes(d Day) int {
	n := 0
	for _, a := range d.Assignments {
		if a.Status != "SKIPPED" && a.Status != "MISSED" && a.Status != "BLOCKED" {
			n += a.DurationMinutes
		}
	}
	return n
}
func makePlan(today string, settings Settings, mockMinutes int) Plan {
	p := Plan{ID: uuid.NewString(), Revision: 1, StartDate: today, EndDate: addDays(today, 29), Days: []Day{}, Weeks: []Week{}, Changes: []Change{}}
	for i := 0; i < 30; i++ {
		p.Days = append(p.Days, Day{Date: addDays(today, i), DayIndex: i + 1, Week: i/7 + 1, Assignments: []Assignment{}})
	}
	for w := 1; w <= 5; w++ {
		end := addDays(today, w*7-1)
		if end > p.EndDate {
			end = p.EndDate
		}
		status := "DISABLED"
		if settings.WeeklyMockEnabled && w <= 4 {
			status = "AVAILABLE"
		}
		p.Weeks = append(p.Weeks, Week{Week: w, StartDate: addDays(today, (w-1)*7), EndDate: end, Goal: "Практика по разделам и просмотр разбора; Full Mock по желанию", Preliminary: w > 1, MockOffer: MockOffer{Status: status, DurationMinutes: mockMinutes, Target: "/full-mocks"}})
	}
	return p
}

// fillFuture is deterministic apart from opaque IDs. Past work, explicit skips,
// started work and reviewed history survive replanning. No missed work is debt.
func fillFuture(p *Plan, settings Settings, from, today string, materials []material, results []result, reentry bool) {
	generic := []material{}
	for _, m := range materials {
		if m.Unit == nil || !m.Unit.Drill.MasteryEligible {
			generic = append(generic, m)
		}
	}
	materials = generic
	used := map[string]string{}
	reviewed := map[string]bool{}
	skillUse := map[string]int{}
	weekPrimary := map[int]int{}
	attempted := map[string]bool{}
	hasEvidence := map[string]bool{}
	wrong := map[string]int{}
	latestEvidence := map[string]time.Time{}
	weekSkillUse := map[int]map[string]int{}
	titles := map[string]string{}
	for _, m := range materials {
		titles[m.Skill+":"+m.ID] = m.Title
	}
	reserved := map[string]bool{}
	active := map[string]bool{}
	for _, r := range results {
		if r.Status == "IN_PROGRESS" || r.Status == "PROCESSING" {
			active[r.ID] = true
		}
		if r.Status == "PROCESSING" || (r.Status == "IN_PROGRESS" && r.Claimed) {
			reserved[r.Skill+":"+r.MaterialID] = true
		}
		attempted[r.Skill+":"+r.MaterialID] = true
		if r.Status == "SUBMITTED" || r.Status == "ABANDONED" {
			key := r.Skill + ":" + r.MaterialID
			if !r.StartedAt.IsZero() && r.StartedAt.Format(time.DateOnly) > used[key] {
				used[key] = r.StartedAt.Format(time.DateOnly)
			}
		}
		if r.Status == "SUBMITTED" {
			hasEvidence[r.Skill] = true
			when := r.StartedAt
			if r.SubmittedAt != nil {
				when = *r.SubmittedAt
			}
			if !when.Before(latestEvidence[r.Skill]) {
				latestEvidence[r.Skill] = when
				wrong[r.Skill] = 0
				if dayGap(when.Format(time.DateOnly), today) <= 14 {
					wrong[r.Skill] = r.Wrong
				}
			}
			if r.SubmittedAt != nil {
				key := r.Skill + ":" + r.MaterialID
				day := r.SubmittedAt.Format(time.DateOnly)
				if day > used[key] {
					used[key] = day
				}
			}
		}
	}
	// The latest answer to each immutable question supersedes its historical
	// mistake for priority. Lifetime wrong totals remain available only in review.
	type qState struct {
		when    time.Time
		wrong   bool
		section string
	}
	recentQuestions := map[string]qState{}
	questionSections := map[string]bool{}
	for _, r := range results {
		if r.Status != "SUBMITTED" {
			continue
		}
		when := r.StartedAt
		if r.SubmittedAt != nil {
			when = *r.SubmittedAt
		}
		for _, q := range r.Questions {
			questionSections[r.Skill] = true
			key := r.Skill + ":" + q.ID
			if prior, ok := recentQuestions[key]; !ok || when.After(prior.when) {
				recentQuestions[key] = qState{when, !q.Correct, r.Skill}
			}
		}
	}
	for section := range questionSections {
		wrong[section] = 0
	}
	for _, q := range recentQuestions {
		if q.wrong && dayGap(q.when.Format(time.DateOnly), today) >= 0 && dayGap(q.when.Format(time.DateOnly), today) <= 14 {
			wrong[q.section]++
		}
	}
	for _, d := range p.Days {
		for _, a := range d.Assignments {
			unsuitable := a.Status == "SKIPPED" && a.SkipReason != nil && *a.SkipReason == "unsuitable"
			if a.AttemptID != nil && active[*a.AttemptID] && a.MaterialID != nil {
				reserved[a.Skill+":"+*a.MaterialID] = true
			}
			if d.Date < from || terminal(a.Status) || a.AttemptID != nil || a.SessionID != nil || a.CycleID != nil {
				if (a.MaterialID != nil || a.DrillID != nil) && ((a.Status != "MISSED" && a.Status != "SKIPPED") || a.AttemptID != nil || unsuitable) {
					id := a.MaterialID
					if id == nil {
						id = a.DrillID
					}
					key := a.Skill + ":" + *id
					if d.Date > used[key] {
						used[key] = d.Date
					}
					skillUse[a.Skill]++
					weekPrimary[d.Week]++
					if weekSkillUse[d.Week] == nil {
						weekSkillUse[d.Week] = map[string]int{}
					}
					weekSkillUse[d.Week][a.Skill]++
				}
				// An explicit rejection suppresses this review for the cycle;
				// it does not acknowledge viewing or contribute to progress.
				if a.SourceAttemptID != nil && (a.Status == "COMPLETED" || unsuitable || (a.Status == "PLANNED" && d.Date >= today && d.Date < from)) {
					reviewed[*a.SourceAttemptID] = true
				}
			}
		}
	}
	for di := range p.Days {
		d := &p.Days[di]
		d.Preliminary = d.Week > (dayGap(p.StartDate, today)/7 + 1)
		if p.ExamDate != nil && d.Date >= *p.ExamDate {
			continue
		}
		if d.Date < from {
			continue
		}
		kept := []Assignment{}
		for _, a := range d.Assignments {
			if terminal(a.Status) || a.AttemptID != nil || a.SessionID != nil || a.CycleID != nil {
				kept = append(kept, a)
			}
		}
		d.Assignments = kept
		anchored := false
		for _, a := range kept {
			if a.AttemptID != nil || a.SessionID != nil || a.Status == "COMPLETED" {
				anchored = true
			}
		}
		if !anchored {
			d.IsRest = !selected(settings, d.Date)
			d.BudgetMinutes = settings.DailyMinutes
			if d.IsRest {
				d.BudgetMinutes = 0
			}
		}
		for _, w := range p.Weeks {
			if w.MockOffer.Status == "SCHEDULED" && w.MockOffer.ScheduledDate != nil && *w.MockOffer.ScheduledDate == d.Date {
				d.BudgetMinutes = *w.MockOffer.WindowMinutes
				d.IsRest = false
			}
		}
		mockDay := false
		for _, w := range p.Weeks {
			if w.MockOffer.Status == "SCHEDULED" && w.MockOffer.ScheduledDate != nil && *w.MockOffer.ScheduledDate == d.Date {
				mockDay = true
			}
		}
		if reentry && d.Date == today && !mockDay {
			d.IsRest = false
			if d.BudgetMinutes == 0 {
				d.BudgetMinutes = settings.DailyMinutes
			}
		}
		if d.IsRest || mockDay {
			d.PlannedMinutes = plannedMinutes(*d)
			continue
		}
		budget := d.BudgetMinutes - plannedMinutes(*d)
		isReturn := reentry && d.Date == today
		if isReturn && budget > 30 {
			budget = 30
		}
		candidates := append([]material{}, materials...)
		sort.SliceStable(candidates, func(i, j int) bool {
			x, y := candidates[i], candidates[j]
			// Coverage precedes weakness: historical mistakes cannot starve a section.
			if skillUse[x.Skill] != skillUse[y.Skill] {
				return skillUse[x.Skill] < skillUse[y.Skill]
			}
			if hasEvidence[x.Skill] != hasEvidence[y.Skill] {
				return !hasEvidence[x.Skill]
			}
			if (wrong[x.Skill] > 0) != (wrong[y.Skill] > 0) {
				return wrong[x.Skill] > 0
			}
			if skillUse[x.Skill] != skillUse[y.Skill] {
				return skillUse[x.Skill] < skillUse[y.Skill]
			}
			if (x.DrillID == "") != (y.DrillID == "") {
				return x.DrillID == ""
			}
			if attempted[x.Skill+":"+x.ID] != attempted[y.Skill+":"+y.ID] {
				return !attempted[x.Skill+":"+x.ID]
			}
			if x.Duration != y.Duration {
				return x.Duration < y.Duration
			}
			return x.Skill+":"+x.ID < y.Skill+":"+y.ID
		})
		// One primary material per day spreads a small bank across the week.
		// The budget is a ceiling, never a requirement to consume more materials.
		primary, reviewCount := false, 0
		for _, a := range kept {
			if a.Status == "SKIPPED" || a.Status == "MISSED" {
				continue
			}
			if a.Kind == "REVIEW" {
				reviewCount++
			} else if a.MaterialID != nil || a.DrillID != nil {
				primary = true
			}
		}
		eligible := []material{}
		for _, m := range candidates {
			key := m.Skill + ":" + m.ID
			if !reserved[key] && (used[key] == "" || dayGap(used[key], d.Date) >= 7) && m.Duration <= budget && m.Duration > 0 {
				eligible = append(eligible, m)
			}
		}
		var reviewCandidate *result
		if reviewCount == 0 && budget >= 10 {
			for i := range results {
				r := &results[i]
				if !r.Reviewable || r.Status != "SUBMITTED" || reviewed[r.ID] || (r.Wrong == 0 && len(improvements(r.Feedback)) == 0) {
					continue
				}
				if !isReturn && (r.SubmittedAt == nil || dayGap(r.SubmittedAt.Format(time.DateOnly), d.Date) < reviewDelay(*r)) {
					continue
				}
				reviewCandidate = r
				break
			}
		}
		lastStudy := lastStudyDay(*p, settings, d.Week)
		// Keep the final available material for the weekly check/repeat. This
		// leaves room for review on the preceding day instead of exhausting the
		// bank early and then blocking the whole end of the week.
		deferLast := len(eligible) == 1 && weekPrimary[d.Week] > 0 && d.Date < lastStudy && reviewCandidate != nil && !isReturn
		if !primary && len(eligible) > 0 && !deferLast {
			m := eligible[0]
			key := m.Skill + ":" + m.ID
			kind := "PRACTICE"
			reason := "Практика по разделу: завершенных работ по этому разделу пока нет"
			if hasEvidence[m.Skill] {
				reason = "Практика по разделу для поддержания навыка"
				if wrong[m.Skill] > 0 {
					reason = fmt.Sprintf("Приоритет раздела по сохраненным результатам: неверных ответов — %d. Последняя работа за 14 дней; приоритет ограничен поддержанием остальных секций", wrong[m.Skill])
				}
			}
			if attempted[key] || used[key] != "" {
				reason += ". Повторная практика с интервалом от 7 дней; не используется для независимой оценки текущего уровня"
			} else if d.Date == lastStudy {
				kind = "CHECKPOINT"
				reason += ". Недельная проверка на новом материале; только учебный результат, а не официальный балл IELTS"
			}
			if isReturn {
				kind = "REENTRY"
				reason = "Возвращение после перерыва от 7 дней; одно короткое упражнение, без накопленного долга"
			}
			if m.Passage {
				reason += ". Отдельный текст; на задания без таймера выделяется 20 минут практики"
			}
			a := newAssignment(kind, m.Skill, m.Title, reason, m.Duration)
			if m.DrillID != "" {
				a.Kind = "DRILL"
				a.DrillID = ptr(m.DrillID)
				a.Target = "/study-plan/learning/drills/" + m.DrillID
				a.Reason += ". Отдельное короткое упражнение; редакторский черновик не подтверждает независимое освоение."
			} else {
				a.MaterialID = ptr(m.ID)
				a.Target = "/exam/" + m.Skill + "/" + m.ID
			}
			d.Assignments = append(d.Assignments, a)
			used[key] = d.Date
			skillUse[m.Skill]++
			weekPrimary[d.Week]++
			budget -= m.Duration
			primary = true
		}
		if settings.DailyMinutes >= 60 && primary && !isReturn {
			if weekSkillUse[d.Week] == nil {
				weekSkillUse[d.Week] = map[string]int{}
			}
			// Count today's primary before filling a second component maintenance floor.
			for _, a := range d.Assignments {
				if a.Kind != "REVIEW" && (a.MaterialID != nil || a.DrillID != nil) && a.Status != "SKIPPED" && a.Status != "MISSED" {
					weekSkillUse[d.Week][a.Skill]++
				}
			}
			extra := append([]material{}, eligible...)
			sort.SliceStable(extra, func(i, j int) bool {
				return weekSkillUse[d.Week][extra[i].Skill] < weekSkillUse[d.Week][extra[j].Skill]
			})
			for _, m := range extra {
				key := m.Skill + ":" + m.ID
				if weekSkillUse[d.Week][m.Skill] >= 2 || m.Duration > budget || used[key] == d.Date {
					continue
				}
				reason := "Поддержание всех секций в пределах дневного бюджета"
				if used[key] != "" || attempted[key] {
					reason += ". Повторная практика с интервалом от 7 дней; не используется для независимой оценки"
				}
				a := newAssignment("PRACTICE", m.Skill, m.Title, reason, m.Duration)
				if m.DrillID != "" {
					a.Kind = "DRILL"
					a.DrillID = ptr(m.DrillID)
					a.Target = "/study-plan/learning/drills/" + m.DrillID
				} else {
					a.MaterialID = ptr(m.ID)
					a.Target = "/exam/" + m.Skill + "/" + m.ID
				}
				d.Assignments = append(d.Assignments, a)
				used[key] = d.Date
				skillUse[m.Skill]++
				weekSkillUse[d.Week][m.Skill]++
				weekPrimary[d.Week]++
				budget -= m.Duration
				break
			}
		}
		// Reviews follow executable practice. One 10-minute block is about a
		// quarter of a 45-minute day; a 30-minute day may still fit 20+10.
		reviewLimit := d.BudgetMinutes / 4
		if reviewLimit < 10 {
			reviewLimit = 10
		}
		if reviewCandidate != nil && reviewLimit >= 10 && budget >= 10 && !(isReturn && primary) {
			r := reviewCandidate
			title := r.Title
			if title == "" {
				title = titles[r.Skill+":"+r.MaterialID]
			}
			if title == "" {
				title = skillName(r.Skill)
			}
			if r.SubmittedAt != nil {
				title += " · " + r.SubmittedAt.Format(time.DateOnly)
			}
			a := newAssignment("REVIEW", r.Skill, "Разбор: "+title, reviewReason(*r), 10)
			a.SourceAttemptID = ptr(r.ID)
			a.Target = "/attempts/" + r.ID
			d.Assignments = append(d.Assignments, a)
			reviewed[r.ID] = true
		}
		if len(d.Assignments) == 0 {
			kind := "PRACTICE"
			if isReturn {
				kind = "REENTRY"
			}
			a := newAssignment(kind, "reading", "Практика пока недоступна", blockedReason(materials, used, reserved, d.Date, budget, deferLast, lastStudy), 0)
			a.Status = "BLOCKED"
			a.Target = "/practice"
			d.Assignments = append(d.Assignments, a)
		}
		d.PlannedMinutes = plannedMinutes(*d)
	}
	for i := range p.Weeks {
		if p.Weeks[i].EndDate >= from {
			p.Weeks[i].Goal = weeklyGoal(p.Weeks[i].Week, hasEvidence, wrong)
		}
	}
}
func change(p *Plan, kind, description, today string) {
	p.Changes = append(p.Changes, Change{Type: kind, Description: description, Date: ptr(today)})
}
func reconcileAttempts(p *Plan, results []result) bool {
	byID := map[string]result{}
	for _, r := range results {
		byID[r.ID] = r
	}
	changed := false
	for di := range p.Days {
		for ai := range p.Days[di].Assignments {
			a := &p.Days[di].Assignments[ai]
			if a.Kind == "REVIEW" || a.AttemptID == nil || a.Status == "SKIPPED" {
				continue
			}
			r, ok := byID[*a.AttemptID]
			if !ok {
				continue
			}
			status := a.Status
			switch r.Status {
			case "IN_PROGRESS":
				status = "IN_PROGRESS"
			case "PROCESSING":
				status = "AWAITING_RESULT"
			case "SUBMITTED":
				status = "COMPLETED"
			case "ABANDONED":
				if !terminal(a.Status) {
					status = "BLOCKED"
				}
			}
			if status != a.Status {
				a.Status = status
				changed = true
			}
			if status == "COMPLETED" && a.CompletedAt == nil && r.SubmittedAt != nil {
				a.CompletedAt = ptr(r.SubmittedAt.UTC().Format(time.RFC3339))
				changed = true
			}
		}
	}
	return changed
}
func reconcileDay(p *Plan, today string) bool {
	changed := false
	for di := range p.Days {
		d := &p.Days[di]
		for ai := range d.Assignments {
			a := &d.Assignments[ai]
			if d.Date < today && (a.Status == "PLANNED" || a.Status == "BLOCKED") && a.AttemptID == nil && a.SessionID == nil {
				a.Status = "MISSED"
				changed = true
			}
		}
		d.PlannedMinutes = plannedMinutes(*d)
	}
	return changed
}
func reviewReason(r result) string {
	if r.Wrong > 0 {
		return fmt.Sprintf("Неверных ответов в завершенной попытке — %d. Просмотр разбора фиксирует ознакомление, а не освоение", r.Wrong)
	}
	return "Рекомендация из разбора ИИ: " + improvements(r.Feedback)[0] + ". Просмотр фиксирует ознакомление, а не освоение или независимую оценку"
}

func lastStudyDay(p Plan, settings Settings, week int) string {
	last := ""
	for _, d := range p.Days {
		if d.Week == week && selected(settings, d.Date) {
			last = d.Date
		}
	}
	return last
}
func skillName(skill string) string {
	switch skill {
	case "listening":
		return "Listening"
	case "reading":
		return "Reading"
	case "writing":
		return "Writing"
	case "speaking":
		return "Speaking"
	default:
		return "Раздел"
	}
}
func weeklyGoal(week int, hasEvidence map[string]bool, wrong map[string]int) string {
	phases := []string{"Начальная практика и сбор результатов", "Практика и исправление по разборам", "Отложенная практика на доступных материалах", "Недельная проверка и повторение", "Итоги 30 дней и выбор следующего шага"}
	missing, errors := []string{}, []string{}
	for _, skill := range skills {
		if !hasEvidence[skill] {
			missing = append(missing, skillName(skill))
		}
		if wrong[skill] > 0 {
			errors = append(errors, fmt.Sprintf("%s — %d", skillName(skill), wrong[skill]))
		}
	}
	goal := phases[week-1]
	if len(missing) > 0 {
		goal += ". Пока нет завершенных работ: " + strings.Join(missing, ", ")
	}
	if len(errors) > 0 {
		goal += ". Сохраненные неверные ответы по разделам: " + strings.Join(errors, "; ") + ". Типы ошибок не размечены"
	} else {
		goal += ". Данных о неверных ответах для выбора узких навыков пока нет"
	}
	return goal + ". Будущие результаты не предполагаются; задания зависят от доступного банка"
}
func blockedReason(materials []material, used map[string]string, reserved map[string]bool, today string, budget int, deferLast bool, lastStudy string) string {
	if len(materials) == 0 {
		return "Нет подходящих опубликованных материалов для выбранного типа экзамена. Откройте практику или обновите профиль"
	}
	next, fits, active := "", false, false
	for _, m := range materials {
		if m.Duration <= 0 || m.Duration > budget {
			continue
		}
		fits = true
		key := m.Skill + ":" + m.ID
		if reserved[key] {
			active = true
			continue
		}
		available := today
		if used[key] != "" && addDays(used[key], 7) > available {
			available = addDays(used[key], 7)
		}
		if deferLast && available == today {
			available = lastStudy
		}
		if next == "" || available < next {
			next = available
		}
	}
	if next != "" {
		return "Банк материалов ограничен; ближайшая доступность с учетом недельного распределения и интервала повторения — " + next + ". Заполнять весь бюджет не требуется"
	}
	if active && fits {
		return "Подходящие материалы уже связаны с незавершенными попытками; продолжите начатую работу в практике"
	}
	return "Опубликованные материалы есть, но целиком не помещаются в доступное время. Измените бюджет или выберите другое упражнение в практике"
}

func reviewDelay(r result) int {
	if r.FromMock {
		return 1
	}
	return 2
}
