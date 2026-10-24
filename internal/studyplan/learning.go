package studyplan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func correctionDay(st *state, c *Correction) string {
	t, err := time.Parse(time.RFC3339, c.SavedAt)
	if err != nil {
		return ""
	}
	z, err := time.LoadLocation(st.Learning.Zone)
	if err != nil {
		z = time.UTC
	}
	return localDate(t, z)
}

func sectionForSkill(id string) string {
	for _, s := range microSkills {
		if s.ID == id {
			return s.Section
		}
	}
	return ""
}
func deadlineReached(goal Goal, today string) bool {
	return goal.ExamDate != nil && today >= *goal.ExamDate
}
func unitByID(units []drillUnit, id string) *drillUnit {
	for i := range units {
		if units[i].Drill.ID == id {
			return &units[i]
		}
	}
	return nil
}
func cycleByID(st *state, id string) *learningCycle {
	for i := range st.Learning.Cycles {
		if st.Learning.Cycles[i].Cycle.ID == id {
			return &st.Learning.Cycles[i]
		}
	}
	return nil
}
func sessionByID(st *state, id string) *learningSession {
	for i := range st.Learning.Sessions {
		if st.Learning.Sessions[i].Session.ID == id {
			return &st.Learning.Sessions[i]
		}
	}
	return nil
}
func familySeen(st *state, family *string) bool {
	if family == nil {
		return true
	}
	for _, x := range st.Learning.Sessions {
		if x.Session.Drill.FamilyID != nil && *x.Session.Drill.FamilyID == *family {
			return true
		}
	}
	for _, c := range st.Learning.Cycles {
		if c.Cycle.Source.FamilyID != nil && *c.Cycle.Source.FamilyID == *family {
			return true
		}
	}
	return false
}

func syncLearning(st *state, sources []sourceMistake, units []drillUnit, today string) {
	previous := map[string][]byte{}
	for _, c := range st.Learning.Cycles {
		raw, _ := json.Marshal(c.Cycle)
		previous[c.Cycle.ID] = raw
	}
	if st.Learning.Cycles == nil {
		st.Learning.Cycles = []learningCycle{}
	}
	if st.Learning.Sessions == nil {
		st.Learning.Sessions = []learningSession{}
	}
	if st.Learning.Receipts == nil {
		st.Learning.Receipts = map[string]learningReceipt{}
	}
	for _, src := range sources {
		found := false
		for i := range st.Learning.Cycles {
			c := &st.Learning.Cycles[i]
			if c.SourceKey == src.Identity {
				found = true
				// Editorial provenance may be corrected/withdrawn after the mistake occurred.
				c.Cycle.Source.FamilyID = src.Source.FamilyID
				c.Cycle.Source.FamilyKnown = src.Source.FamilyKnown
				break
			}
		}
		if found {
			continue
		}
		st.Learning.Cycles = append(st.Learning.Cycles, learningCycle{Cycle: Cycle{ID: uuid.NewString(), Revision: 1, SkillID: src.SkillID, Section: sectionForSkill(src.SkillID), Status: "needs-practice", Source: src.Source, Checks: []TransferCheck{}, RemainingChecks: 2, Limitations: []string{"Внутреннее правило практики, а не подтверждённый балл IELTS. Неизвестное семейство не подтверждает независимость."}}, Key: src.Key, SourceKey: src.Identity})
	}
	for i := range st.Learning.Cycles {
		c := &st.Learning.Cycles[i].Cycle
		if c.Source.SessionID != nil {
			saved := sessionByID(st, *c.Source.SessionID)
			c.Source.FamilyKnown = false
			c.Source.FamilyID = nil
			if saved != nil {
				u := unitByID(units, saved.Session.Drill.ID)
				if u != nil && u.Drill.MasteryEligible && drillContentHash(*u) == drillContentHash(drillUnit{saved.Session.Drill, saved.Key, saved.TeacherFeedback}) {
					c.Source.FamilyKnown = true
					c.Source.FamilyID = u.Drill.FamilyID
				}
			}
		}
	}
	for i := range st.Learning.Sessions {
		ss := &st.Learning.Sessions[i]
		if ss.Session.Status != "submitted" || !ss.Session.Independent {
			continue
		}
		u := unitByID(units, ss.Session.Drill.ID)
		if u == nil || !u.Drill.MasteryEligible || !sameUnit(*u, *ss) {
			ss.Session.Independent = false
			ss.Session.IndependenceReason = "Редакторское подтверждение материала изменилось; результат сохранён как практика."
			ss.Session.Revision++
		}
	}
	for i := range st.Learning.Cycles {
		refreshCycle(st, &st.Learning.Cycles[i], units, today)
	}
	for i := range st.Learning.Cycles {
		c := &st.Learning.Cycles[i].Cycle
		if before, ok := previous[c.ID]; ok {
			after, _ := json.Marshal(c)
			if string(before) != string(after) {
				c.Revision++
			}
		}
	}

}

func refreshCycle(st *state, lc *learningCycle, units []drillUnit, today string) {
	c := &lc.Cycle
	previousNext := c.NextDrillID
	c.NextDrillID = nil
	c.NextCheckDate = nil
	families := map[string]bool{}
	delayed := false
	for i := range c.Checks {
		check := &c.Checks[i]
		session := sessionByID(st, check.SessionID)
		check.Independent = false
		if session != nil {
			u := unitByID(units, session.Session.Drill.ID)
			check.Independent = u != nil && sameUnit(*u, *session) && u.Drill.MasteryEligible && session.Session.Independent && c.Source.FamilyKnown && !sourceOriginMatches(*c, session.Session.Drill) && c.Source.FamilyID != nil && check.FamilyID != nil && *check.FamilyID != *c.Source.FamilyID
			if !check.Independent {
				check.Reason = "Только практика: недостаточно действующих редакторских подтверждений, данных о семействе или независимости."
			}
		}
		if check.Independent && check.Passed && check.FamilyID != nil {
			families[*check.FamilyID] = true
			delayed = delayed || check.Delayed
		}
	}
	c.RemainingChecks = 2 - len(families)
	if c.RemainingChecks < 0 {
		c.RemainingChecks = 0
	}
	if c.RemainingChecks == 0 && !delayed {
		c.RemainingChecks = 1
	}
	c.Status = "needs-practice"
	if c.Correction != nil {
		if c.Correction.Verdict == "correct" && !c.Correction.Assisted {
			c.Status = "awaiting-transfer"
			c.NextCheckDate = ptr(addDays(correctionDay(st, c.Correction), 3))
			if c.RemainingChecks == 0 {
				c.Status = "demonstrated"
			}
		} else if c.Correction.Verdict == "unverified" {
			c.Status = "correction-saved"
		} else if c.Correction.Verdict == "correct" {
			c.Status = "correction-saved"
		}
	}
	if c.SkippedAt != nil || c.Status == "demonstrated" {
		return
	}
	if c.Status == "awaiting-transfer" {
		if previousNext != nil {
			u := unitByID(units, *previousNext)
			if u != nil && u.Drill.MasteryEligible && !familySeen(st, u.Drill.FamilyID) && !knownSessionOrigin(st, u.Drill) && !sourceOriginMatches(*c, u.Drill) {
				c.NextDrillID = previousNext
			}
		}
		return
	}
	candidates := append([]drillUnit{}, units...)
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Drill, candidates[j].Drill
		if a.MasteryEligible != b.MasteryEligible {
			return !a.MasteryEligible
		}
		if familySeen(st, a.FamilyID) != familySeen(st, b.FamilyID) {
			return !familySeen(st, a.FamilyID)
		}
		return a.ID < b.ID
	})
	for _, u := range candidates {
		d := u.Drill
		// Reserved checks are not remedial practice before an own correct correction.
		if d.MasteryEligible {
			continue
		}
		if d.SkillID == c.SkillID && d.Modality != "script" && !sourceOriginMatches(*c, d) && (c.Source.FamilyID == nil || d.FamilyID == nil || *c.Source.FamilyID != *d.FamilyID) {
			c.NextDrillID = ptr(d.ID)
			break
		}
	}
}
func sameUnit(u drillUnit, s learningSession) bool {
	// Bind qualifications to exact content/key/feedback, not just mutable IDs.
	a, _ := json.Marshal(struct {
		D Drill
		K json.RawMessage
		F string
	}{u.Drill, u.Key, u.Feedback})
	b, _ := json.Marshal(struct {
		D Drill
		K json.RawMessage
		F string
	}{s.Session.Drill, s.Key, s.TeacherFeedback})
	return string(a) == string(b)
}
func learningInventory(settings Settings, materials []material, units []drillUnit) Inventory {
	inv := Inventory{DailyMinutes: settings.DailyMinutes, Sections: []SectionInventory{}, Limitations: []string{"Права, ключи, доступность аудио и полнота форм требуют редакторской проверки. Количество материалов показывает доступность, а не доказанное покрытие Academic."}}
	for _, section := range skills {
		s := SectionInventory{Section: section, Limitations: []string{}}
		families := map[string]bool{}
		for _, m := range materials {
			if m.Skill == section && m.DrillID == "" {
				s.FullMaterials++
				if m.Duration <= settings.DailyMinutes {
					s.FittingMaterials++
				}
			}
		}
		for _, u := range units {
			d := u.Drill
			if d.Section != section || d.Modality == "script" {
				continue
			}
			if d.DurationMinutes <= settings.DailyMinutes {
				s.ShortDrills++
			}
			if d.EditorialStatus == "reviewed" {
				s.ReviewedDrills++
			}
			if d.MasteryEligible && d.FamilyID != nil {
				families[*d.FamilyID] = true
			}
		}
		s.IndependentFamilies = len(families)
		s.Available = s.FittingMaterials+s.ShortDrills > 0
		if !s.Available {
			s.Limitations = append(s.Limitations, "Нет исполнимого задания в выбранном бюджете; минимум практики этой секции недоступен.")
		}
		if s.ReviewedDrills == 0 {
			s.Limitations = append(s.Limitations, "Нет проверенных коротких заданий; черновая практика не подтверждает независимый перенос.")
		}
		if section == "listening" {
			s.Limitations = append(s.Limitations, "Текстовые сценарии не являются свидетельством аудирования. Для аудиозаданий нужен доступный опубликованный файл; длительность означает рекомендуемое окно практики, а не измеренную длину записи.")
		}
		if section == "speaking" {
			s.Limitations = append(s.Limitations, "Напечатанные планы не оценивают речь. Связанные записи сохраняются как непроверенные образцы; соответствие новому заданию не подтверждено.")
		}
		if section == "writing" || section == "speaking" {
			s.Limitations = append(s.Limitations, "Правки ожидают квалифицированной независимой проверки; автоматического подтверждения освоения или итогового балла нет.")
		}
		inv.Sections = append(inv.Sections, s)
	}
	return inv
}
func learningResponse(st *state, in inputs, today string) Learning {
	performance := in.TaskPerformance
	if performance == nil {
		performance = taskPerformance(nil)
	}
	out := Learning{Revision: st.Plan.Revision, Today: today, ExamDate: in.Goal.ExamDate, ReadOnly: deadlineReached(in.Goal, today), Skills: microSkills, Profiles: []WeakProfile{}, Cycles: []Cycle{}, Drills: []Drill{}, Inventory: learningInventory(st.Settings, in.Materials, in.Drills), Limitations: []string{"Статусы отражают внутренние правила практики, а не подтверждённые баллы IELTS или прогноз роста.", "Для статуса «Показано на новых заданиях» нужна верная самостоятельная правка и две независимые проверки на новых проверенных семействах, одна — спустя минимум 3 календарных дня.", "Замечания ИИ и правки Writing/Speaking остаются непроверенными; экспертная оценка пока не подключена."}}
	out.TaskPerformance = performance
	for _, c := range st.Learning.Cycles {
		out.Cycles = append(out.Cycles, c.Cycle)
	}
	for _, d := range in.Drills {
		projected := d.Drill
		if projected.MasteryEligible && !knownSessionOrigin(st, projected) && !priorAttemptOrigin(in, projected, time.Time{}) {
			projected.Stimulus = ""
			projected.Task = ""
			projected.AudioURL = nil
		}
		out.Drills = append(out.Drills, projected)
	}
	for _, skill := range microSkills {
		p := WeakProfile{SkillID: skill.ID, Section: skill.Section, Status: "insufficient", SourceIDs: []string{}, Rationale: "Для этого микронавыка пока нет конкретной ошибки или явной рекомендации по улучшению.", RemainingChecks: 2}
		var latest *Cycle
		fam := map[string]bool{}
		for i := range out.Cycles {
			c := &out.Cycles[i]
			if c.SkillID != skill.ID {
				continue
			}
			if latest == nil || c.Source.OccurredAt > latest.Source.OccurredAt || (c.Source.OccurredAt == latest.Source.OccurredAt && c.ID > latest.ID) {
				latest = c
			}

		}
		if latest != nil {
			p.Status = latest.Status
			p.SourceIDs = []string{latest.ID}
			p.UpdatedAt = ptr(cycleUpdatedAt(*latest))
			for _, ck := range latest.Checks {
				if ck.Independent && ck.Passed && ck.FamilyID != nil {
					fam[*ck.FamilyID] = true
					p.DelayedCheckPassed = p.DelayedCheckPassed || ck.Delayed
				}
			}
			p.Rationale = latest.Source.Feedback
			if p.Rationale == "" {
				p.Rationale = "Неверный ответ в исходном задании; полезна практика соответствующего типа вопросов."
			}
			p.RemainingChecks = latest.RemainingChecks
		}
		p.IndependentFamilies = len(fam)
		out.Profiles = append(out.Profiles, p)
	}
	return out
}

func (s *Service) Learning(ctx context.Context, user uuid.UUID, lc *learningCommand) (any, error) {
	_, _, err := s.execute(ctx, user, command{action: "learning", learning: lc})
	return lc.Out, err
}
func validKey(v string) bool { return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= 100 }
func validText(v string, max int) bool {
	return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= max
}
func (s *Service) learningAction(ctx context.Context, tx pgx.Tx, user uuid.UUID, st *state, in inputs, today string, now time.Time, lc *learningCommand) (bool, error) {
	input := lc.Input
	stamp := now.UTC().Format(time.RFC3339)
	if lc.Action == "get" {
		lc.Out = learningResponse(st, in, today)
		return false, nil
	}
	if lc.Action == "inventory" {
		lc.Out = learningInventory(st.Settings, in.Materials, in.Drills)
		return false, nil
	}
	var c *learningCycle
	var session *learningSession
	var unit *drillUnit
	switch lc.Action {
	case "cycle", "view", "correction", "skip":
		c = cycleByID(st, lc.ID)
		if c == nil {
			return false, ErrNotFound
		}
	case "session", "submit":
		session = sessionByID(st, lc.ID)
		if session == nil {
			return false, ErrNotFound
		}
	case "start":
		unit = unitByID(in.Drills, lc.ID)
		if unit == nil {
			return false, ErrNotFound
		}
	default:
		return false, ErrValidation
	}
	if lc.Action == "cycle" {
		lc.Out = c.Cycle
		return false, nil
	}
	if lc.Action == "session" {
		lc.Out = session.Session
		return false, nil
	}
	if st.Plan.ID == "" {
		return false, ErrNotFound
	}
	receiptKey := ""
	hash := ""
	if lc.Action != "view" {
		if !validKey(input.IdempotencyKey) {
			return false, ErrValidation
		}
		rawInput := input
		rawInput.ExpectedRevision = 0
		raw, _ := json.Marshal(rawInput)
		hash = fmt.Sprintf("%x", sha256.Sum256(raw))
		receiptKey = lc.Action + ":" + lc.ID + ":" + input.IdempotencyKey
		if r, ok := st.Learning.Receipts[receiptKey]; ok {
			if r.Hash != hash {
				return false, ErrConflict
			}
			lc.Out = r.Value
			lc.ReceiptReplay = true
			return false, nil
		}
	}
	if deadlineReached(in.Goal, today) {
		return false, ErrConflict
	}
	switch lc.Action {
	case "view":
		if input.ExpectedRevision != c.Cycle.Revision {
			return false, ErrConflict
		}
		if c.Cycle.ViewedAt != nil {
			lc.Out = c.Cycle
			return false, nil
		}
		c.Cycle.ViewedAt = ptr(stamp)
		c.Cycle.Revision++
		lc.Out = c.Cycle
	case "skip":
		if input.ExpectedRevision != c.Cycle.Revision {
			return false, ErrConflict
		}
		if c.Cycle.SkippedAt == nil {
			c.Cycle.SkippedAt = ptr(stamp)
			c.Cycle.Revision++
		}
		lc.Out = c.Cycle
	case "correction":
		if input.ExpectedRevision != c.Cycle.Revision || c.Cycle.SkippedAt != nil {
			return false, ErrConflict
		}
		if !validText(input.Text, 12000) || !validText(input.Explanation, 4000) || input.Assisted == nil {
			return false, ErrValidation
		}
		verdict, feedback := "unverified", "Правка сохранена, но пока не проверена. Нужна экспертная или независимая проверка; автоматического зачёта критерия нет."
		if len(c.Key) > 0 {
			verdict = "incorrect"
			if objectiveCorrect(c.Key, input.Text) {
				verdict = "correct"
			}
			feedback = objectiveFeedback(verdict, c.Cycle.Source.Feedback)
		}
		correction := Correction{input.Text, input.Explanation, *input.Assisted, stamp, verdict, feedback}
		c.Revisions = append(c.Revisions, correction)
		c.Cycle.Correction = &correction
		c.Cycle.Checks = []TransferCheck{}
		c.Cycle.Revision++
		refreshCycle(st, c, in.Drills, today)
		lc.Out = c.Cycle
	case "start":

		if unit.Drill.DurationMinutes > st.Settings.DailyMinutes {
			return false, ErrConflict
		}
		if input.AssignmentID != nil {
			a, day := learningAssignment(st, *input.AssignmentID)
			if a == nil {
				return false, ErrNotFound
			}
			if day != today || a.DrillID == nil || *a.DrillID != unit.Drill.ID || terminal(a.Status) {
				return false, ErrConflict
			}
			if input.CycleID != nil && !sameOptional(input.CycleID, a.CycleID) {
				return false, ErrConflict
			}
			input.CycleID = a.CycleID
			if a.SessionID != nil {
				saved := sessionByID(st, *a.SessionID)
				if saved != nil {
					lc.Out = saved.Session
					return false, nil
				}
			}
		}
		if input.CycleID != nil {
			c = cycleByID(st, *input.CycleID)
			if c == nil {
				return false, ErrNotFound
			}
			if c.Cycle.SkippedAt != nil || c.Cycle.SkillID != unit.Drill.SkillID || c.Cycle.Correction == nil {
				return false, ErrConflict
			}
			// A cycle link must never silently spend an unseen control on an
			// incorrect/assisted correction. Explicit free practice has no cycle.
			if unit.Drill.MasteryEligible && c.Cycle.Status != "awaiting-transfer" {
				return false, ErrConflict
			}
		}

		if input.AssignmentID != nil && input.CycleID != nil {
			a, _ := learningAssignment(st, *input.AssignmentID)
			if a == nil || !assignmentBindingValid(st, *a, *unit, c) || !transferDrillEligible(st, in, c, *unit, now) || c.Cycle.NextCheckDate == nil || today < *c.Cycle.NextCheckDate {
				return false, ErrConflict
			}
		}
		// A different open task for the cycle must be resolved before starting another.
		if input.CycleID != nil && hasOpenCycleSession(st, *input.CycleID) {
			for _, saved := range st.Learning.Sessions {
				if saved.Session.Status == "in-progress" && saved.Session.Drill.ID == unit.Drill.ID && sameOptional(saved.Session.CycleID, input.CycleID) && sameOptional(saved.Session.AssignmentID, input.AssignmentID) {
					lc.Out = saved.Session
					return false, nil
				}
			}
			return false, ErrConflict
		}

		if input.ExpectedRevision != st.Plan.Revision {
			return false, ErrConflict
		}
		// Reload/new idempotency key resumes an owned open session for this drill/context.
		for _, saved := range st.Learning.Sessions {
			if saved.Session.Status == "in-progress" && saved.Session.Drill.ID == unit.Drill.ID && sameOptional(saved.Session.CycleID, input.CycleID) && sameOptional(saved.Session.AssignmentID, input.AssignmentID) {
				lc.Out = saved.Session
				return false, nil
			}
		}
		ss := learningSession{Session: Session{ID: uuid.NewString(), Revision: 1, Drill: unit.Drill, CycleID: input.CycleID, AssignmentID: input.AssignmentID, Status: "in-progress", StartedAt: stamp, IndependenceReason: "Независимость проверяется при отправке ответа."}, Key: unit.Key, TeacherFeedback: unit.Feedback, SeenBefore: familySeen(st, unit.Drill.FamilyID) || knownSessionOrigin(st, unit.Drill) || priorAttemptOrigin(in, unit.Drill, now)}
		if c != nil {
			ss.CycleRevision = c.Cycle.Revision
			ss.CorrectionGeneration = len(c.Revisions)
		}
		st.Learning.Sessions = append(st.Learning.Sessions, ss)
		if input.AssignmentID != nil {
			a, _ := learningAssignment(st, *input.AssignmentID)
			a.SessionID = ptr(ss.Session.ID)
			a.Status = "IN_PROGRESS"
		}
		lc.Out = ss.Session
	case "submit":
		ss := &session.Session
		if input.ExpectedRevision != ss.Revision || ss.Status != "in-progress" {
			return false, ErrConflict
		}
		if ss.AssignmentID != nil {
			a, _ := learningAssignment(st, *ss.AssignmentID)
			if a != nil && (a.Status == "SKIPPED" || a.Status == "MISSED") {
				return false, ErrConflict
			}
		}
		if input.Assisted == nil || utf8.RuneCountInString(input.Explanation) > 4000 {
			return false, ErrValidation
		}
		if ss.Drill.Modality == "audio" && ss.Drill.Section == "speaking" {
			if input.AudioAttemptID == nil {
				return false, ErrValidation
			}
			id, err := uuid.Parse(*input.AudioAttemptID)
			if err != nil {
				return false, ErrValidation
			}
			var own bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attempts a JOIN speaking_recordings sr ON sr.attempt_id=a.id WHERE a.id=$1 AND a.user_id=$2 AND a.material_type='speaking' AND a.status='SUBMITTED' AND sr.byte_size>0 AND NOT EXISTS(SELECT 1 FROM full_mock_session_sections sec JOIN full_mock_sessions fm ON fm.id=sec.session_id WHERE sec.attempt_id=a.id AND fm.status<>'SUBMITTED'))`, id, user).Scan(&own)
			if err != nil {
				return false, err
			}
			if !own {
				return false, ErrNotFound
			}
		} else {
			if !validText(input.Text, 12000) || input.AudioAttemptID != nil {
				return false, ErrValidation
			}
		}
		verdict, feedback := "unverified", session.TeacherFeedback
		if len(session.Key) > 0 {
			verdict = "incorrect"
			if objectiveCorrect(session.Key, input.Text) {
				verdict = "correct"
			}
			feedback = objectiveFeedback(verdict, session.TeacherFeedback)
		}
		ss.Text = ptr(input.Text)
		ss.Explanation = ptr(input.Explanation)
		ss.Assisted = *input.Assisted
		ss.AudioAttemptID = input.AudioAttemptID
		ss.Verdict = ptr(verdict)
		ss.Feedback = ptr(feedback)
		ss.SubmittedAt = ptr(stamp)
		ss.Status = "submitted"
		ss.Revision++
		ss.Independent = false
		ss.IndependenceReason = "Только практика: для независимой проверки нужны проверенное объективное задание, известное новое семейство, отсутствие прежних просмотров и помощи, а также неизменённая верная исходная правка."
		current := unitByID(in.Drills, ss.Drill.ID)
		if ss.CycleID != nil {
			c = cycleByID(st, *ss.CycleID)
			if c != nil {
				sameCorrection := len(c.Revisions) == session.CorrectionGeneration && c.Cycle.Correction != nil && c.Cycle.Correction.Verdict == "correct" && !c.Cycle.Correction.Assisted && c.Cycle.SkippedAt == nil
				ss.Independent = sameCorrection && current != nil && sameUnit(*current, *session) && ss.Drill.MasteryEligible && !session.SeenBefore && !priorAttemptOrigin(in, ss.Drill, mustTimestamp(ss.StartedAt)) && !sourceOriginMatches(c.Cycle, ss.Drill) && !*input.Assisted && c.Cycle.Source.FamilyKnown && c.Cycle.Source.FamilyID != nil && ss.Drill.FamilyID != nil && *ss.Drill.FamilyID != *c.Cycle.Source.FamilyID
				if ss.Independent {
					ss.IndependenceReason = "Проверенное задание того же микронавыка из ранее не встречавшегося семейства; помощь не заявлена."
				}
				delayed := sameCorrection && dayGap(correctionDay(st, c.Cycle.Correction), today) >= 3
				c.Cycle.Checks = append(c.Cycle.Checks, TransferCheck{ss.ID, ss.Drill.FamilyID, stamp, ss.Independent, delayed, verdict == "correct", ss.IndependenceReason})
				c.Cycle.Revision++
				refreshCycle(st, c, in.Drills, today)
			}
		}
		if ss.AssignmentID != nil {
			a, _ := learningAssignment(st, *ss.AssignmentID)
			if a != nil && a.Status != "SKIPPED" {
				a.Status = "COMPLETED"
				a.CompletedAt = ptr(stamp)
			}
		}
		// Incorrect objective drills create their own concrete correction cycle.
		if verdict == "incorrect" {
			section := ss.Drill.Section
			modality := "objective"
			if ss.Drill.Modality == "script" {
				modality = "script"
			}
			src := SourceEvidence{AudioURL: ss.Drill.AudioURL, AttemptID: "", SessionID: ptr(ss.ID), DrillID: ptr(ss.Drill.ID), QuestionID: ss.Drill.SourceQuestionID, MaterialID: ss.Drill.ID, VersionID: ss.Drill.Version, FamilyID: ss.Drill.FamilyID, FamilyKnown: current != nil && current.Drill.MasteryEligible && drillContentHash(*current) == drillContentHash(drillUnit{ss.Drill, session.Key, session.TeacherFeedback}), QuestionType: ss.Drill.SkillID, Prompt: ss.Drill.Task, Stimulus: ss.Drill.Stimulus, OriginalAnswer: input.Text, Feedback: session.TeacherFeedback, OccurredAt: stamp, Modality: modality}
			st.Learning.Cycles = append(st.Learning.Cycles, learningCycle{Cycle: Cycle{ID: uuid.NewString(), Revision: 1, SkillID: ss.Drill.SkillID, Section: section, Status: "needs-practice", Source: src, Checks: []TransferCheck{}, RemainingChecks: 2, Limitations: []string{"Оригинальный учебный черновик не является валидированным контролем."}}, Key: session.Key, SourceKey: "session:" + ss.ID})
		}
		lc.Out = *ss
	}
	if receiptKey != "" {
		raw, _ := json.Marshal(lc.Out)
		st.Learning.Receipts[receiptKey] = learningReceipt{hash, raw}
	}
	st.LastActivity = today
	return true, nil
}
func sameOptional(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func learningAssignment(st *state, id string) (*Assignment, string) {
	for di := range st.Plan.Days {
		for ai := range st.Plan.Days[di].Assignments {
			a := &st.Plan.Days[di].Assignments[ai]
			if a.ID == id {
				return a, st.Plan.Days[di].Date
			}
		}
	}
	return nil, ""
}
func objectiveFeedback(verdict, teacher string) string {
	if teacher == "" {
		teacher = "Опирайтесь на точную формулировку источника и отличайте сведения текста от домысла. Правка проверяется по сохранённому ключу вопроса."
	}
	label := "Ответ не совпал с ключом задания."
	if verdict == "correct" {
		label = "Ответ совпал с ключом задания."
	}
	return label + " " + teacher
}

// Objective correction accepts plain value/option IDs, or the existing JSON answer
// object for multiple-select. It never accepts a client-supplied correctness flag.
func objectiveCorrect(key json.RawMessage, text string) bool {
	var correct map[string]any
	if json.Unmarshal(key, &correct) != nil {
		return false
	}
	given := objectiveGiven(correct, text)
	if v, ok := correct["optionId"].(string); ok {
		return v != "" && given["optionId"] == v
	}
	if values, ok := correct["optionIds"].([]any); ok {
		actual, ok := given["optionIds"].([]any)
		if !ok || len(values) == 0 || len(actual) != len(values) {
			return false
		}
		seen := map[string]int{}
		for _, a := range actual {
			s, ok := a.(string)
			if !ok {
				return false
			}
			seen[s]++
		}
		for _, v := range values {
			s, ok := v.(string)
			if !ok || seen[s] == 0 {
				return false
			}
			seen[s]--
		}
		return true
	}
	normalize := func(v string) string {
		v = strings.ToLower(strings.Join(strings.Fields(v), " "))
		if v == "not_given" {
			v = "not given"
		}
		return v
	}
	value, _ := given["value"].(string)
	if normalize(value) == "" {
		return false
	}
	if expected, ok := correct["value"].(string); ok {
		if normalize(expected) != "" && normalize(value) == normalize(expected) {
			return true
		}
	}
	if accepted, ok := correct["accepted"].([]any); ok {
		for _, a := range accepted {
			if s, ok := a.(string); ok && normalize(s) == normalize(value) {
				return true
			}
		}
	}
	return false
}

func mustTimestamp(v string) time.Time { t, _ := time.Parse(time.RFC3339, v); return t }
