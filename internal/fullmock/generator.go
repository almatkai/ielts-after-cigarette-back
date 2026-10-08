package fullmock

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const generatedDurationMinutes = 165

var mockSkills = []string{"listening", "reading", "writing", "speaking"}

// GeneratorRepository owns atomic selection + creation: a second tab must resume
// the same exam, not generate a second draw. No catalog record is created.
type GeneratorRepository interface {
	MockOverview(context.Context, uuid.UUID) (Overview, error)
	GenerateSession(context.Context, uuid.UUID, bool) (Session, bool, error)
}

type candidate struct {
	skill      string
	materialID uuid.UUID
	versionID  uuid.UUID
	completed  bool
	previous   bool
}

// The same pool feeds both the overview and the draw. Single reading passages
// and short listening exercises are practice, not full exam sections.
const eligibleMaterials = `
 SELECT 'listening' AS skill, m.id AS material_id, v.id AS version_id
 FROM listening_tests m JOIN listening_test_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED'
 AND (SELECT count(*) FROM listening_parts p WHERE p.test_version_id=v.id)=4
 AND (SELECT COALESCE(sum(q.points),0) FROM listening_parts p
      JOIN listening_question_groups g ON g.part_id=p.id JOIN listening_questions q ON q.group_id=g.id
      WHERE p.test_version_id=v.id)=40
 UNION ALL
 SELECT 'reading', m.id, v.id
 FROM reading_materials m JOIN reading_material_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED' AND m.exam_type=$2 AND m.material_kind='TEST'
 AND (SELECT count(*) FROM reading_test_passages p WHERE p.test_material_version_id=v.id)=3
 AND (SELECT COALESCE(sum(q.points),0) FROM reading_test_passages p
      JOIN reading_question_groups g ON g.material_version_id=p.passage_material_version_id
      JOIN reading_questions q ON q.group_id=g.id WHERE p.test_material_version_id=v.id)=40
 UNION ALL
 SELECT 'writing', m.id, v.id
 FROM writing_materials m JOIN writing_material_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED' AND v.exam_type=$2 AND jsonb_array_length(v.tasks)=2
 UNION ALL
 SELECT 'speaking', m.id, v.id
 FROM speaking_materials m JOIN speaking_material_versions v ON v.id=m.published_version_id
 WHERE m.status='PUBLISHED' AND jsonb_array_length(v.parts)=3
`

const candidateQuery = `WITH eligible AS (` + eligibleMaterials + `), previous_session AS (
 SELECT id FROM full_mock_sessions WHERE user_id=$1 ORDER BY started_at DESC, id DESC LIMIT 1
)
SELECT e.skill, e.material_id, e.version_id,
 EXISTS (SELECT 1 FROM attempts a WHERE a.user_id=$1 AND a.material_type=e.skill
   AND a.material_id=e.material_id AND a.status IN ('SUBMITTED','PROCESSING')),
 EXISTS (SELECT 1 FROM full_mock_session_sections sec JOIN attempts a ON a.id=sec.attempt_id
   WHERE sec.session_id IN (SELECT id FROM previous_session) AND a.material_type=e.skill AND a.material_id=e.material_id)
FROM eligible e ORDER BY e.skill, e.material_id`

type generatorQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadCandidates(ctx context.Context, q generatorQuerier, userID uuid.UUID) (string, []candidate, error) {
	var examType string
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT exam_type FROM user_profiles WHERE user_id=$1),'')`, userID).Scan(&examType)
	if err != nil {
		return "", nil, err
	}
	if examType != "academic" && examType != "general" {
		return "", nil, nil
	}
	rows, err := q.Query(ctx, candidateQuery, userID, examType)
	if err != nil {
		return "", nil, fmt.Errorf("load mock bank: %w", err)
	}
	defer rows.Close()
	items := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.skill, &item.materialID, &item.versionID, &item.completed, &item.previous); err != nil {
			return "", nil, err
		}
		items = append(items, item)
	}
	return examType, items, rows.Err()
}

func bankStatuses(items []candidate) ([]BankStatus, bool) {
	banks := make([]BankStatus, 0, 4)
	ready := true
	for _, skill := range mockSkills {
		bank := BankStatus{Skill: skill}
		for _, item := range items {
			if item.skill != skill {
				continue
			}
			bank.Total++
			if item.completed {
				bank.Completed++
			}
		}
		bank.Remaining = bank.Total - bank.Completed
		bank.IsExhausted = bank.Total > 0 && bank.Remaining == 0
		if bank.Total == 0 {
			ready = false
		}
		banks = append(banks, bank)
	}
	return banks, ready
}

// Never trade an unseen material for a repeat just to avoid the previous draw.
func selectionPool(items []candidate, skill string) []candidate {
	all, unseen, other := []candidate{}, []candidate{}, []candidate{}
	for _, item := range items {
		if item.skill != skill {
			continue
		}
		all = append(all, item)
		if !item.completed {
			unseen = append(unseen, item)
		}
		if !item.previous {
			other = append(other, item)
		}
	}
	if len(unseen) > 0 {
		return unseen
	}
	if len(other) > 0 {
		return other
	}
	return all
}

func (r *PostgresRepository) MockOverview(ctx context.Context, userID uuid.UUID) (Overview, error) {
	examType, items, err := loadCandidates(ctx, r.pool, userID)
	if err != nil {
		return Overview{}, err
	}
	banks, ready := bankStatuses(items)
	result := Overview{ExamType: examType, DurationMinutes: generatedDurationMinutes, Banks: banks, Ready: ready}
	active, err := r.getSession(ctx, `SELECT `+sessionColumns+` FROM full_mock_sessions
 WHERE user_id=$1 AND status='IN_PROGRESS' ORDER BY started_at DESC, id DESC LIMIT 1`, userID)
	if err == nil {
		result.ActiveSession = &active
	} else if !errors.Is(err, ErrSessionNotFound) {
		return Overview{}, err
	}
	return result, nil
}

func (r *PostgresRepository) GenerateSession(ctx context.Context, userID uuid.UUID, restart bool) (Session, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, false, err
	}
	defer tx.Rollback(ctx)
	// All generated starts/restarts for a user serialize, even before a row exists.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "fullmock:"+userID.String()); err != nil {
		return Session{}, false, err
	}
	var activeID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM full_mock_sessions WHERE user_id=$1 AND status='IN_PROGRESS'
 ORDER BY started_at DESC, id DESC LIMIT 1 FOR UPDATE`, userID).Scan(&activeID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, err
	}
	if activeID != uuid.Nil && !restart {
		if err := tx.Commit(ctx); err != nil {
			return Session{}, false, err
		}
		session, err := r.GetSession(ctx, activeID)
		return session, false, err
	}
	examType, items, err := loadCandidates(ctx, tx, userID)
	if err != nil {
		return Session{}, false, err
	}
	if examType == "" {
		return Session{}, false, ErrExamTypeRequired
	}
	_, ready := bankStatuses(items)
	if !ready {
		return Session{}, false, ErrBankIncomplete
	}
	selected := make([]candidate, 0, 4)
	for _, skill := range mockSkills {
		pool := selectionPool(items, skill)
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(pool))))
		if err != nil {
			return Session{}, false, err
		}
		selected = append(selected, pool[index.Int64()])
	}
	// Validate every pool before replacing the active exam. Failure rolls back all
	// changes; a missing bank must not destroy a student's in-progress answers.
	if restart {
		if _, err := tx.Exec(ctx, `UPDATE attempts SET status='ABANDONED', submitted_at=CURRENT_TIMESTAMP
 WHERE status='IN_PROGRESS' AND id IN (SELECT sec.attempt_id FROM full_mock_session_sections sec
 JOIN full_mock_sessions s ON s.id=sec.session_id WHERE s.user_id=$1 AND s.status='IN_PROGRESS')`, userID); err != nil {
			return Session{}, false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE full_mock_sessions SET status='ABANDONED', submitted_at=CURRENT_TIMESTAMP
 WHERE user_id=$1 AND status='IN_PROGRESS'`, userID); err != nil {
			return Session{}, false, err
		}
	}
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO full_mock_sessions (id,user_id,exam_type,title,duration_minutes)
 VALUES ($1,$2,$3,'Полный пробный IELTS',$4)`, id, userID, examType, generatedDurationMinutes); err != nil {
		return Session{}, false, err
	}
	for i, item := range selected {
		attemptID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO attempts (id,user_id,material_type,material_id,material_version_id)
 VALUES ($1,$2,$3,$4,$5)`, attemptID, userID, item.skill, item.materialID, item.versionID); err != nil {
			return Session{}, false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO full_mock_session_sections (session_id,position,skill,attempt_id)
 VALUES ($1,$2,$3,$4)`, id, i+1, item.skill, attemptID); err != nil {
			return Session{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, false, err
	}
	session, err := r.GetSession(ctx, id)
	return session, true, err
}

func (s *Service) Overview(ctx context.Context, userID uuid.UUID) (Overview, error) {
	repo, ok := s.repository.(GeneratorRepository)
	if !ok {
		return Overview{}, fmt.Errorf("full mock generator is unavailable")
	}
	for i := 0; i < 16; i++ {
		result, err := repo.MockOverview(ctx, userID)
		if err != nil {
			return Overview{}, err
		}
		if result.ActiveSession == nil {
			return result, nil
		}
		session, err := s.decorate(ctx, userID, *result.ActiveSession)
		if err != nil {
			return Overview{}, err
		}
		if session.Status == SessionInProgress {
			result.ActiveSession = &session
			return result, nil
		}
		// Expiration can complete persisted answers. Refresh counts and check
		// the next legacy session rather than hiding an older active exam.
	}
	return Overview{}, fmt.Errorf("too many expired mock sessions")
}

func (s *Service) StartGenerated(ctx context.Context, userID uuid.UUID, restart bool) (Session, bool, error) {
	repo, ok := s.repository.(GeneratorRepository)
	if !ok {
		return Session{}, false, fmt.Errorf("full mock generator is unavailable")
	}
	// Legacy users can have several active exams. Expire them through the normal
	// grader before drawing, rather than ignoring deadlines or discarding drafts.
	for i := 0; i < 16; i++ {
		session, created, err := repo.GenerateSession(ctx, userID, restart)
		if err != nil {
			return Session{}, false, err
		}
		session, err = s.decorate(ctx, userID, session)
		if err != nil {
			return Session{}, false, err
		}
		if session.Status == SessionInProgress {
			return session, created, nil
		}
	}
	return Session{}, false, fmt.Errorf("too many expired mock sessions")
}

func (s *Service) sessionTest(ctx context.Context, session Session) (Test, error) {
	if session.MockTestID == uuid.Nil {
		return Test{ExamType: session.ExamType, Title: session.Title, DurationMinutes: session.DurationMinutes, Status: StatusPublished}, nil
	}
	test, err := s.repository.Get(ctx, session.MockTestID)
	if err != nil {
		return Test{}, err
	}
	if session.DurationMinutes > 0 {
		test.DurationMinutes = session.DurationMinutes
		test.ExamType = session.ExamType
		test.Title = session.Title
	}
	return test, nil
}
