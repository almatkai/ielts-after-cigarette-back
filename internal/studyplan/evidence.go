package studyplan

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Feedback is persisted AI output, not a fresh model call or a claim of mastery.
// Only explicit improvement strings are recommendations. Never fabricate them.
func improvements(raw []byte) []string {
	var feedback struct {
		Improvements []string `json:"improvements"`
		Tasks        []struct {
			Improvements []string `json:"improvements"`
		} `json:"tasks"`
		Parts []struct {
			Improvements []string `json:"improvements"`
		} `json:"parts"`
	}
	if json.Unmarshal(raw, &feedback) != nil {
		return nil
	}
	values := feedback.Improvements
	for _, t := range feedback.Tasks {
		values = append(values, t.Improvements...)
	}
	for _, p := range feedback.Parts {
		values = append(values, p.Improvements...)
	}
	out := []string{}
	for _, s := range values {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		r := []rune(s)
		if len(r) > 400 {
			s = string(r[:400]) + "…"
		}
		out = append(out, s)
		if len(out) == 3 {
			break
		}
	}
	return out
}
func evidence(results []result, now time.Time) []Evidence {
	out := make([]Evidence, 0, 4)
	for _, skill := range skills {
		e := Evidence{Skill: skill, Status: "NO_EVIDENCE", Source: "none", Limitations: []string{"Недостаточно данных для пересчета в балл; ориентировочный балл недоступен"}}
		distinct := map[string]bool{}
		var latest, submitted *result
		for i := range results {
			r := &results[i]
			if r.Skill != skill {
				continue
			}
			if r.Status == "SUBMITTED" {
				distinct[r.MaterialID] = true
				if submitted == nil {
					submitted = r
				}
			}
			if latest == nil && (r.Status == "SUBMITTED" || r.Status == "PROCESSING") {
				latest = r
			}
		}
		e.SampleCount = len(distinct)
		if latest != nil {
			r := latest
			if latest.Status == "PROCESSING" && submitted != nil {
				r = submitted
			}
			e.AttemptID = ptr(r.ID)
			e.Source = "practice"
			e.Status = "PRACTICE_UNVALIDATED"
			if r.Status == "PROCESSING" {
				e.Status = "AWAITING_RESULT"
				e.Limitations = append(e.Limitations, "Оценивание выполняется или завершилось сбоем; нулевой балл не выставляется")
			} else {
				e.Score = r.Score
				e.MaxScore = r.MaxScore
				if r.SubmittedAt != nil {
					e.AssessedAt = ptr(r.SubmittedAt.UTC().Format(time.RFC3339))
					if now.Sub(*r.SubmittedAt) >= 28*24*time.Hour {
						e.Status = "STALE"
						e.Limitations = append(e.Limitations, "Последний учебный результат получен 28 дней назад или больше; сохранено историческое значение")
					}
				}
				if skill == "writing" || skill == "speaking" {
					e.Source = "ai_practice"
					e.Score = nil
					e.MaxScore = nil
					e.Limitations = append(e.Limitations, "Разбор практики сгенерирован ИИ, а не экзаменатором IELTS; числовая оценка скрыта до валидации")
					if r.Band == nil {
						e.Status = "ASSESSMENT_UNAVAILABLE"
						e.Limitations = append(e.Limitations, "Оценка от ИИ недоступна; нулевой балл не выставляется")
					}
				} else {
					e.Limitations = append(e.Limitations, "Ответы получены в режиме практики; материал может быть неполным, а шкала пересчета не валидирована")
				}
				if r.Repeat {
					e.Limitations = append(e.Limitations, "Повторная практика материала, включая другие версии; не используется для независимой оценки текущего уровня")
				}
			}
			if latest.Status == "PROCESSING" {
				e.Status = "AWAITING_RESULT"
				e.Limitations = append(e.Limitations, "Попытка "+latest.ID+" ожидает оценивания; это не нулевой балл. Показанные результат и дата относятся к предыдущей завершенной попытке")
			}
		}
		out = append(out, e)
	}
	return out
}
func recommendations(results []result) []Recommendation {
	out := []Recommendation{}
	for _, r := range results {
		if r.Status != "SUBMITTED" || !r.Reviewable {
			continue
		}
		if r.Wrong > 0 {
			out = append(out, Recommendation{ID: r.ID + ":wrong", Skill: r.Skill, Title: fmt.Sprintf("Просмотр разбора: неверных ответов — %d", r.Wrong), Reason: reviewReason(r), AttemptID: ptr(r.ID), Target: "/attempts/" + r.ID})
		}
		for i, text := range improvements(r.Feedback) {
			out = append(out, Recommendation{ID: fmt.Sprintf("%s:ai:%d", r.ID, i), Skill: r.Skill, Title: text, Reason: "Рекомендация зафиксирована в разборе ИИ; не является подтвержденным диагнозом", AttemptID: ptr(r.ID), Target: "/attempts/" + r.ID})
		}
		if len(out) >= 12 {
			break
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}
func progress(p *Plan, results []result, zone *time.Location) []Progress {
	out := []Progress{}
	byDate := map[string]*Progress{}
	if p != nil {
		for _, d := range p.Days {
			out = append(out, Progress{Date: d.Date, PlannedMinutes: d.PlannedMinutes})
		}
		for i := range out {
			byDate[out[i].Date] = &out[i]
		}
	}
	seen := map[string]bool{}
	for _, r := range results {
		if r.Status != "SUBMITTED" || r.SubmittedAt == nil || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		if day := byDate[localDate(*r.SubmittedAt, zone)]; day != nil {
			day.CompletedAttempts++
		}
	}
	if p != nil {
		for _, d := range p.Days {
			for _, a := range d.Assignments {
				if a.Status == "SKIPPED" && (a.SkipReason == nil || *a.SkipReason != "superseded_by_optional_mock") {
					byDate[d.Date].SkippedAssignments++
				}
				if a.Kind == "REVIEW" && a.Status == "COMPLETED" && a.CompletedAt != nil {
					at, err := time.Parse(time.RFC3339, *a.CompletedAt)
					if err == nil {
						if day := byDate[localDate(at, zone)]; day != nil {
							day.ReviewedAssignments++
						}
					}
				}
			}
		}
	}
	return out
}
