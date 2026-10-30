package studyplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AttemptStarter is implemented by the existing attempts.Service. In particular
// Start resumes the user's standalone draft rather than manufacturing a result.
type AttemptStarter interface {
	Start(context.Context, uuid.UUID, string, uuid.UUID) (attempts.Attempt, any, bool, error)
}
type MockOverview interface {
	MockOverview(context.Context, uuid.UUID) (fullmock.Overview, error)
}
type Service struct {
	pool     *pgxpool.Pool
	attempts AttemptStarter
	mocks    MockOverview
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, starter AttemptStarter, mocks MockOverview) *Service {
	return &Service{pool: pool, attempts: starter, mocks: mocks, now: time.Now}
}

type command struct {
	learning      *learningCommand
	action        string
	settings      Settings
	expected      int64
	assignment    string
	reason        string
	week          int
	mockAction    string
	scheduledDate string
	window        int
}

func (s *Service) Get(ctx context.Context, user uuid.UUID) (Response, error) {
	r, _, e := s.execute(ctx, user, command{action: "get"})
	return r, e
}
func (s *Service) Create(ctx context.Context, user uuid.UUID, settings Settings) (Response, error) {
	if err := validateSettings(settings); err != nil {
		return Response{}, err
	}
	r, _, e := s.execute(ctx, user, command{action: "create", settings: settings})
	return r, e
}
func (s *Service) Settings(ctx context.Context, user uuid.UUID, settings Settings, revision int64) (Response, error) {
	if err := validateSettings(settings); err != nil {
		return Response{}, err
	}
	r, _, e := s.execute(ctx, user, command{action: "settings", settings: settings, expected: revision})
	return r, e
}
func (s *Service) Replan(ctx context.Context, user uuid.UUID, revision int64) (Response, error) {
	r, _, e := s.execute(ctx, user, command{action: "replan", expected: revision})
	return r, e
}
func (s *Service) Start(ctx context.Context, user uuid.UUID, id string, revision int64) (StartResponse, error) {
	_, r, e := s.execute(ctx, user, command{action: "start", assignment: id, expected: revision})
	return r, e
}
func (s *Service) Skip(ctx context.Context, user uuid.UUID, id string, revision int64, reason string) (Response, error) {
	if reason != "no_time" && reason != "unsuitable" && reason != "outside_app" && reason != "other" {
		return Response{}, ErrValidation
	}
	r, _, e := s.execute(ctx, user, command{action: "skip", assignment: id, expected: revision, reason: reason})
	return r, e
}
func (s *Service) Review(ctx context.Context, user uuid.UUID, id string, revision int64) (Response, error) {
	r, _, e := s.execute(ctx, user, command{action: "review", assignment: id, expected: revision})
	return r, e
}
func (s *Service) Mock(ctx context.Context, user uuid.UUID, week int, revision int64, action, scheduledDate string, window int) (Response, error) {
	if action != "schedule" && action != "decline" {
		return Response{}, ErrValidation
	}
	r, _, e := s.execute(ctx, user, command{action: "mock", week: week, expected: revision, mockAction: action, scheduledDate: scheduledDate, window: window})
	return r, e
}

// beginLocked serializes each user's aggregate, including absent-row creates.
// A lock waiter must release its connection: the owner can call repositories
// that need another pool connection for mock availability or attempt starts.
// Blocking advisory-lock waiters would otherwise exhaust the pool and prevent
// the owner from completing. The database lock remains authoritative across
// service instances; cancellation also interrupts the retry delay.
func (s *Service) beginLocked(ctx context.Context, user uuid.UUID) (pgx.Tx, error) {
	for {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		var locked bool
		err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,43))`, user.String()).Scan(&locked)
		if err == nil && locked {
			return tx, nil
		}
		rollbackErr := tx.Rollback(ctx)
		if err != nil {
			return nil, err
		}
		if rollbackErr != nil {
			return nil, rollbackErr
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) execute(ctx context.Context, user uuid.UUID, c command) (response Response, start StartResponse, err error) {
	tx, err := s.beginLocked(ctx, user)
	if err != nil {
		return response, start, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	in, err := loadInputs(ctx, tx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return response, start, err
	}
	zone, zoneErr := time.LoadLocation(in.Zone)
	if zoneErr != nil {
		zone = time.UTC
		in.Zone = "UTC"
	}
	now := s.now()
	today := localDate(now, zone)
	for i := range in.Results {
		in.Results[i].StartedAt = in.Results[i].StartedAt.In(zone)
		if in.Results[i].SubmittedAt != nil {
			in.Results[i].SubmittedAt = ptr(in.Results[i].SubmittedAt.In(zone))
		}
	}
	var st state
	var raw []byte
	var rev int64
	err = tx.QueryRow(ctx, `SELECT state,revision FROM study_plans WHERE user_id=$1 FOR UPDATE`, user).Scan(&raw, &rev)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return response, start, err
	}
	if exists {
		if err = json.Unmarshal(raw, &st); err != nil {
			return response, start, fmt.Errorf("decode persisted study plan: %w", err)
		}
	}
	if !exists && c.action != "create" && c.action != "get" && c.action != "learning" {
		return response, start, ErrNotFound
	}
	if exists && (c.action == "start" || c.action == "skip" || c.action == "review") {
		found := false
		for _, d := range st.Plan.Days {
			for _, a := range d.Assignments {
				if a.ID == c.assignment {
					found = true
				}
			}
		}
		if !found {
			return response, start, ErrNotFound
		}
	}
	if exists && c.action != "create" && c.action != "get" && c.action != "learning" && (c.expected < 1 || c.expected != rev) {
		return response, start, ErrConflict
	}
	dirty := false
	if !exists && c.action == "learning" {
		st.Settings = defaultSettings()
		st.Learning.Zone = in.Zone
		st.Learning.Cycles = []learningCycle{}
		st.Learning.Sessions = []learningSession{}
		st.Learning.Receipts = map[string]learningReceipt{}
		if c.learning.Action != "get" && c.learning.Action != "inventory" {
			return response, start, ErrNotFound
		}
		_, err = s.learningAction(ctx, tx, user, &st, in, today, now, c.learning)
		return response, start, err
	}
	created := false
	if !exists && c.action == "create" {
		duration := 0
		if s.mocks != nil {
			overview, e := s.mocks.MockOverview(ctx, user)
			if e != nil {
				return response, start, e
			}
			duration = overview.DurationMinutes
		}
		if duration <= 0 {
			return response, start, errors.New("full mock overview duration unavailable")
		}
		st = state{Settings: c.settings, Plan: makePlan(today, c.settings, duration), LastDay: today, LastActivity: today, Inputs: fingerprint(in)}
		st.Plan.ExamDate = in.Goal.ExamDate
		fillFuture(&st.Plan, st.Settings, today, today, in.Materials, learningResults(in.Results, st.Learning), false)
		exists = true
		created = true
		dirty = true
	}
	// Only an explicit create may begin a new expired cycle. Keep the same
	// user aggregate/claim namespace and archive the entire previous cycle;
	// GET never destroys history or silently rolls the plan forward.
	if exists && !created && c.action == "create" && today > st.Plan.EndDate {
		reconcileAttempts(&st.Plan, in.Results)
		reconcileDay(&st.Plan, today)
		st.History = append(st.History, st.Plan)
		duration := st.Plan.Weeks[0].MockOffer.DurationMinutes
		if s.mocks != nil {
			overview, e := s.mocks.MockOverview(ctx, user)
			if e != nil {
				return response, start, e
			}
			duration = overview.DurationMinutes
		}
		id := st.Plan.ID
		st.Plan = makePlan(today, c.settings, duration)
		st.Plan.ID = id
		st.Settings = c.settings
		for _, r := range in.Results {
			activity := localDate(r.StartedAt, zone)
			if activity <= today && activity > st.LastActivity {
				st.LastActivity = activity
			}
			if r.SubmittedAt != nil {
				activity = localDate(*r.SubmittedAt, zone)
				if activity <= today && activity > st.LastActivity {
					st.LastActivity = activity
				}
			}
		}
		st.Plan.ReturnAfterPause = dayGap(st.LastActivity, today) >= 7
		st.Plan.ExamDate = in.Goal.ExamDate
		fillFuture(&st.Plan, st.Settings, today, today, in.Materials, learningResults(in.Results, st.Learning), st.Plan.ReturnAfterPause)
		st.LastDay = today
		st.Inputs = fingerprint(in)
		change(&st.Plan, "NEW_CYCLE", "Новый 30-дневный цикл создан по вашему запросу; история предыдущего цикла сохранена", today)
		dirty = true
	}
	if exists && !created {
		st.Plan.Revision = rev
		if reconcileAttempts(&st.Plan, in.Results) {
			dirty = true
		}
		activity := st.LastActivity
		for _, r := range in.Results {
			value := localDate(r.StartedAt, zone)
			if value <= today && value > activity {
				activity = value
			}
			if r.SubmittedAt != nil {
				value = localDate(*r.SubmittedAt, zone)
				if value <= today && value > activity {
					activity = value
				}
			}
		}
		if activity != st.LastActivity {
			st.LastActivity = activity
			dirty = true
		}
		hash := fingerprint(in)
		if st.LastDay != today {
			missedChanged := reconcileDay(&st.Plan, today)
			wasReturning := st.Plan.ReturnAfterPause
			st.Plan.ReturnAfterPause = dayGap(st.LastActivity, today) >= 7
			from := addDays(today, 1)
			if st.Plan.ReturnAfterPause {
				from = today
				if !wasReturning {
					change(&st.Plan, "REENTRY", "Возвращение после перерыва от 7 дней; без накопленного долга", today)
				}
			}
			st.Plan.ExamDate = in.Goal.ExamDate
			fillFuture(&st.Plan, st.Settings, from, today, in.Materials, learningResults(in.Results, st.Learning), st.Plan.ReturnAfterPause)
			st.LastDay = today
			dirty = true
			if missedChanged {
				change(&st.Plan, "DAY_RECONCILED", "Неначатые прошлые задания отмечены пропущенными; время не переносится", today)
			}
		} else if st.Inputs != hash {
			st.Plan.ExamDate = in.Goal.ExamDate
			fillFuture(&st.Plan, st.Settings, addDays(today, 1), today, in.Materials, learningResults(in.Results, st.Learning), false)
			change(&st.Plan, "NEW_EVIDENCE", "Профиль, банк заданий или результаты попыток изменились; план на будущее обновлен", today)
			dirty = true
		}
		st.Inputs = hash
	}
	if exists {
		beforeLearning, _ := json.Marshal(st.Learning)
		st.Learning.Zone = in.Zone
		syncLearning(&st, in.Sources, in.Drills, today)
		reconcileDrillSessions(&st)
		if reconcileLearningAssignments(&st, in, today, now) {
			dirty = true
		}
		afterLearning, _ := json.Marshal(st.Learning)
		if string(beforeLearning) != string(afterLearning) {
			dirty = true
		}
		st.Plan.ExamDate = in.Goal.ExamDate
		if applyExamDeadline(&st.Plan, in.Goal, today) {
			dirty = true
		}
		switch c.action {
		case "learning":
			// Present the current persisted aggregate revision to start commands.
			changed, e := s.learningAction(ctx, tx, user, &st, in, today, now, c.learning)
			if e != nil {
				return response, start, e
			}
			dirty = dirty || changed
			if changed {
				st.Learning.Zone = in.Zone
				syncLearning(&st, in.Sources, in.Drills, today)
				fillFuture(&st.Plan, st.Settings, addDays(today, 1), today, in.Materials, learningResults(in.Results, st.Learning), false)
				applyExamDeadline(&st.Plan, in.Goal, today)
			}
		case "settings":
			sort.Ints(c.settings.StudyDays)
			st.Settings = c.settings
			for i := range st.Plan.Weeks {
				w := &st.Plan.Weeks[i]
				if w.EndDate <= today || w.MockOffer.Status == "SCHEDULED" || w.MockOffer.Status == "DECLINED" {
					continue
				}
				w.MockOffer.Status = "DISABLED"
				if c.settings.WeeklyMockEnabled && w.Week <= 4 {
					w.MockOffer.Status = "AVAILABLE"
				}
			}
			st.Plan.ExamDate = in.Goal.ExamDate
			fillFuture(&st.Plan, st.Settings, addDays(today, 1), today, in.Materials, learningResults(in.Results, st.Learning), false)
			change(&st.Plan, "SETTINGS", "Настройки обучения изменены только для будущих дней", today)
			dirty = true
		case "replan":
			st.Plan.ExamDate = in.Goal.ExamDate
			fillFuture(&st.Plan, st.Settings, addDays(today, 1), today, in.Materials, learningResults(in.Results, st.Learning), false)
			change(&st.Plan, "REPLAN", "План на будущее обновлен; выполненные задания и пропуски сохранены, без накопленного долга", today)
			dirty = true
		case "mock":
			if deadlineReached(in.Goal, today) || (in.Goal.ExamDate != nil && c.mockAction == "schedule" && c.scheduledDate >= *in.Goal.ExamDate) {
				return response, start, ErrValidation
			}
			changed, e := scheduleMock(&st, c, today)
			if e != nil {
				return response, start, e
			}
			dirty = dirty || changed
		case "start", "skip", "review":
			if deadlineReached(in.Goal, today) {
				return response, start, ErrConflict
			}
			var a *Assignment
			day := ""
			for di := range st.Plan.Days {
				for ai := range st.Plan.Days[di].Assignments {
					candidate := &st.Plan.Days[di].Assignments[ai]
					if candidate.ID == c.assignment {
						a = candidate
						day = st.Plan.Days[di].Date
					}
				}
			}
			if a == nil {
				return response, start, ErrNotFound
			}
			resume := c.action == "start" && a.AttemptID != nil && (a.Status == "IN_PROGRESS" || a.Status == "AWAITING_RESULT")
			if day != today && !resume {
				return response, start, ErrConflict
			}
			switch c.action {
			case "skip":
				if a.Status == "SKIPPED" {
					break
				}
				if terminal(a.Status) || a.Status == "AWAITING_RESULT" {
					return response, start, ErrConflict
				}
				a.Status = "SKIPPED"
				a.SkipReason = ptr(c.reason)
				dirty = true
				change(&st.Plan, "SKIP", "Задание пропущено ("+skipLabel(c.reason)+"); без накопленного долга и зачета внешних результатов", today)
			case "review":
				if a.Kind != "REVIEW" || a.SourceAttemptID == nil {
					return response, start, ErrConflict
				}
				if a.Status == "COMPLETED" {
					break
				}
				if terminal(a.Status) || !reviewAvailable(in.Results, *a.SourceAttemptID) {
					return response, start, ErrConflict
				}
				a.Status = "COMPLETED"
				a.CompletedAt = ptr(now.UTC().Format(time.RFC3339))
				st.LastActivity = today
				st.Plan.ReturnAfterPause = false
				dirty = true
				change(&st.Plan, "VIEWED", "Просмотр разбора попытки завершен; это фиксирует ознакомление, а не освоение или независимую оценку", today)
			case "start":
				start.Target = a.Target
				start.AttemptID = a.AttemptID
				if a.DrillID != nil {
					return response, start, ErrConflict
				}
				if a.Kind == "REVIEW" {
					if a.SourceAttemptID == nil || !reviewAvailable(in.Results, *a.SourceAttemptID) || (terminal(a.Status) && a.Status != "COMPLETED") {
						return response, start, ErrConflict
					}
					start.AttemptID = a.SourceAttemptID
					break
				}
				if a.Status == "BLOCKED" {
					break
				}
				if terminal(a.Status) {
					return response, start, ErrConflict
				}
				if a.MaterialID == nil || !available(in.Materials, a.Skill, *a.MaterialID) {
					return response, start, ErrConflict
				}
				if a.AttemptID != nil {
					break
				}
				if s.attempts == nil {
					return response, start, errors.New("attempt starter unavailable")
				}
				mID, e := uuid.Parse(*a.MaterialID)
				if e != nil {
					return response, start, e
				}
				attempt, _, novel, e := s.attempts.Start(ctx, user, a.Skill, mID)
				if e != nil {
					return response, start, e
				}
				// Validate the authoritative row even if a dependency returns invalid data.
				var actualStatus string
				e = tx.QueryRow(ctx, `SELECT status FROM attempts a WHERE a.id=$1 AND a.user_id=$2 AND a.material_id=$3 AND a.material_type=$4
      AND NOT EXISTS(SELECT 1 FROM full_mock_session_sections sec WHERE sec.attempt_id=a.id)`, attempt.ID, user, mID, a.Skill).Scan(&actualStatus)
				if errors.Is(e, pgx.ErrNoRows) {
					return response, start, ErrConflict
				}
				if e != nil {
					return response, start, e
				}
				if actualStatus != "IN_PROGRESS" {
					return response, start, ErrConflict
				}
				_, e = tx.Exec(ctx, `INSERT INTO study_plan_attempts(assignment_id,plan_id,attempt_id) VALUES($1,$2,$3)`, a.ID, st.Plan.ID, attempt.ID)
				if e != nil {
					var pgErr *pgconn.PgError
					if errors.As(e, &pgErr) && pgErr.Code == "23505" {
						return response, start, ErrConflict
					}
					return response, start, e
				}
				a.AttemptID = ptr(attempt.ID.String())
				a.Status = "IN_PROGRESS"
				start.AttemptID = a.AttemptID
				if !novel {
					a.Reason += ". Продолжение вашей начатой попытки; не является новой независимой попыткой"
				}
				st.LastActivity = today
				st.Plan.ReturnAfterPause = false
				dirty = true
				// Include the just-created draft in the snapshot fingerprint, avoiding a
				// spurious second GET revision merely because Start inserted a row.
				fresh, e := loadInputs(ctx, tx, user)
				if e != nil {
					return response, start, e
				}
				for i := range fresh.Results {
					fresh.Results[i].StartedAt = fresh.Results[i].StartedAt.In(zone)
					if fresh.Results[i].SubmittedAt != nil {
						fresh.Results[i].SubmittedAt = ptr(fresh.Results[i].SubmittedAt.In(zone))
					}
				}
				in = fresh
				st.Inputs = fingerprint(in)
			}
		}
		if reconcileLearningAssignments(&st, in, today, now) {
			dirty = true
		}
		if c.action == "learning" && !c.learning.ReceiptReplay {
			switch c.learning.Action {
			case "cycle", "view", "correction", "skip":
				if lc := cycleByID(&st, c.learning.ID); lc != nil {
					c.learning.Out = lc.Cycle
					key := c.learning.Action + ":" + c.learning.ID + ":" + c.learning.Input.IdempotencyKey
					if saved, ok := st.Learning.Receipts[key]; ok {
						saved.Value, _ = json.Marshal(lc.Cycle)
						st.Learning.Receipts[key] = saved
					}
				}
			}
		}
		for i := range st.Plan.Days {
			st.Plan.Days[i].PlannedMinutes = plannedMinutes(st.Plan.Days[i])
		}
		if dirty {
			if !created {
				st.Plan.Revision = rev + 1
			}
			payload, e := json.Marshal(st)
			if e != nil {
				return response, start, e
			}
			if created {
				_, e = tx.Exec(ctx, `INSERT INTO study_plans(id,user_id,revision,state) VALUES($1,$2,$3,$4)`, st.Plan.ID, user, st.Plan.Revision, payload)
			} else {
				_, e = tx.Exec(ctx, `UPDATE study_plans SET revision=$2,state=$3,updated_at=CURRENT_TIMESTAMP WHERE user_id=$1`, user, st.Plan.Revision, payload)
			}
			if e != nil {
				return response, start, e
			}
		}
		if c.action == "learning" {
			switch c.learning.Action {
			case "get":
				c.learning.Out = learningResponse(&st, in, today)
			case "inventory":
				c.learning.Out = learningInventory(st.Settings, in.Materials, in.Drills)
			}
		}
		response.Plan = &st.Plan
		response.Settings = st.Settings
		start.PlanRevision = st.Plan.Revision
	} else {
		response.Settings = defaultSettings()
	}
	response.Today = today
	response.Timezone = in.Zone
	response.Goal = in.Goal
	response.Evidence = evidence(in.Results, now)
	response.Recommendations = recommendations(in.Results)
	response.Progress = learningProgress(progress(response.Plan, in.Results, zone), st.Learning, zone)
	response.Limitations = []string{
		"Только учебный результат практики: шкала пересчета не подтверждена; итоговый ориентировочный балл IELTS недоступен",
		"Фактическое время занятий недоступно; запланированное время не означает активность. Для Reading без таймера выделяется 20 минут практики",
		"Для Writing/Speaking дата отправки может отражать завершение оценивания, а не точный день сдачи",
		"Просмотр разбора фиксирует ознакомление, а не освоение; повторная практика и версии заданий не дают независимой оценки",
		"Планирование Full Mock на неделю не бронирует материалы и не создает сессию. Запустите экзамен отдельно в разделе Full Mock",
	}
	if in.Goal.ExamDate != nil {
		response.Limitations = append(response.Limitations, "Дата экзамена "+*in.Goal.ExamDate+": с этой даты новые задания и пробные экзамены недоступны; завершение календаря не подтверждает готовность.")
	}
	for _, section := range learningInventory(response.Settings, in.Materials, in.Drills).Sections {
		if !section.Available {
			response.Limitations = append(response.Limitations, section.Section+": минимальное покрытие недоступно в выбранном бюджете и текущем банке.")
		}
	}
	if zoneErr != nil {
		response.Limitations = append(response.Limitations, "Некорректный часовой пояс; используется UTC до исправления в профиле")
	}
	if in.Goal.ExamType != nil && *in.Goal.ExamType == "general" {
		response.Limitations = append(response.Limitations, "План General Training использует только подходящие материалы; недостающие разделы GT блокируются, а не заменяются на Academic")
	}
	if len(in.Materials) == 0 {
		response.Limitations = append(response.Limitations, "Нет подходящих материалов для этого типа экзамена; практические задания заблокированы")
	}
	if err = tx.Commit(ctx); err != nil {
		return Response{}, StartResponse{}, err
	}
	return response, start, nil
}
func available(materials []material, skill, id string) bool {
	for _, m := range materials {
		if m.Skill == skill && m.ID == id {
			return true
		}
	}
	return false
}
func skipLabel(reason string) string {
	labels := map[string]string{
		"no_time": "нет времени", "unsuitable": "задание не подходит",
		"outside_app": "занимался вне приложения", "other": "другая причина",
		"superseded_by_optional_mock": "заменено запланированным Full Mock",
	}
	if label, ok := labels[reason]; ok {
		return label
	}
	return "другая причина"
}

func reviewAvailable(results []result, id string) bool {
	for _, r := range results {
		if r.ID == id && r.Status == "SUBMITTED" && r.Reviewable {
			return true
		}
	}
	return false
}

func scheduleMock(st *state, c command, today string) (bool, error) {
	if c.week < 1 || c.week > 4 || c.week > len(st.Plan.Weeks) {
		return false, ErrValidation
	}
	w := &st.Plan.Weeks[c.week-1]
	if !st.Settings.WeeklyMockEnabled || w.MockOffer.Status == "DISABLED" {
		return false, ErrConflict
	}
	// Offer at this week's last selected study day, not arbitrarily on Monday.
	overview := ""
	for _, d := range st.Plan.Days {
		if d.Week == w.Week && selected(st.Settings, d.Date) {
			overview = d.Date
		}
	}
	if overview == "" || today < overview || today > addDays(w.EndDate, 7) {
		return false, ErrConflict
	}
	if c.mockAction == "decline" {
		if w.MockOffer.Status == "DECLINED" {
			return false, nil
		}
		if w.MockOffer.Status == "SCHEDULED" {
			return false, ErrConflict
		}
		w.MockOffer.Status = "DECLINED"
		change(&st.Plan, "MOCK_DECLINED", "Отказ от Full Mock; без штрафа и накопленного долга", today)
		return true, nil
	}
	scheduled, err := time.Parse(time.DateOnly, c.scheduledDate)
	if err != nil || scheduled.Format(time.DateOnly) != c.scheduledDate || c.scheduledDate <= today || c.scheduledDate > addDays(today, 7) || c.scheduledDate > st.Plan.EndDate || c.window < w.MockOffer.DurationMinutes || c.window > 1440 {
		return false, ErrValidation
	}
	if w.MockOffer.Status == "SCHEDULED" {
		if w.MockOffer.ScheduledDate != nil && *w.MockOffer.ScheduledDate == c.scheduledDate && w.MockOffer.WindowMinutes != nil && *w.MockOffer.WindowMinutes == c.window {
			return false, nil
		}
		return false, ErrConflict
	}
	for _, other := range st.Plan.Weeks {
		if other.MockOffer.Status == "SCHEDULED" && other.MockOffer.ScheduledDate != nil && *other.MockOffer.ScheduledDate == c.scheduledDate {
			return false, ErrConflict
		}
	}
	for di := range st.Plan.Days {
		d := &st.Plan.Days[di]
		if d.Date != c.scheduledDate {
			continue
		}
		for _, a := range d.Assignments {
			if a.AttemptID != nil || a.Status == "COMPLETED" {
				return false, ErrConflict
			}
		}
		for ai := range d.Assignments {
			a := &d.Assignments[ai]
			if a.Status == "PLANNED" || a.Status == "BLOCKED" {
				a.Status = "SKIPPED"
				a.SkipReason = ptr("superseded_by_optional_mock")
			}
		}
		d.BudgetMinutes = c.window
		d.IsRest = false
		d.PlannedMinutes = 0
	}
	w.MockOffer.Status = "SCHEDULED"
	w.MockOffer.ScheduledDate = ptr(c.scheduledDate)
	w.MockOffer.WindowMinutes = ptr(c.window)
	change(&st.Plan, "MOCK_SCHEDULED", fmt.Sprintf("Full Mock запланирован на %s: выделено окно %d мин, обычные задания отменены без долга. Запустите экзамен отдельно в разделе Full Mock", c.scheduledDate, c.window), today)
	return true, nil
}
