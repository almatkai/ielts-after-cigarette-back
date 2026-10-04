package fullmock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func execSeed(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func mockUser(t *testing.T, pool *pgxpool.Pool, examType string) uuid.UUID {
	t.Helper()
	id := testdb.User(t, pool)
	execSeed(t, pool, `INSERT INTO user_profiles (user_id,display_name,exam_type) VALUES ($1,'Student',$2)`, id, examType)
	return id
}

// Real material/version tables, rather than fake candidates, exercise all four
// branches of the eligibility SQL and the FK/version pinning during creation.
func seedMockListening(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, questionCount int) candidate {
	t.Helper()
	id, version := uuid.New(), uuid.New()
	execSeed(t, pool, `INSERT INTO listening_tests (id,slug,exam_type,created_by,updated_by) VALUES ($1,$2,'general',$3,$3)`, id, id.String(), user)
	execSeed(t, pool, `INSERT INTO listening_test_versions (id,test_id,version_number,title,created_by) VALUES ($1,$2,1,'Listening',$3)`, version, id, user)
	for part := 0; part < 4; part++ {
		p, g := uuid.New(), uuid.New()
		execSeed(t, pool, `INSERT INTO listening_parts (id,test_version_id,position) VALUES ($1,$2,$3)`, p, version, part+1)
		execSeed(t, pool, `INSERT INTO listening_question_groups (id,part_id,position,question_type) VALUES ($1,$2,1,'short_answer')`, g, p)
		for n := part * 10; n < (part+1)*10 && n < questionCount; n++ {
			execSeed(t, pool, `INSERT INTO listening_questions (id,group_id,position,number,prompt,answer) VALUES ($1,$2,$3,$4,'Question','{"value":"answer"}')`, uuid.New(), g, n%10+1, n+1)
		}
	}
	execSeed(t, pool, `UPDATE listening_tests SET status='PUBLISHED',current_version_id=$2,published_version_id=$2,published_at=CURRENT_TIMESTAMP WHERE id=$1`, id, version)
	return candidate{skill: "listening", materialID: id, versionID: version}
}

func seedMockReading(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, examType, kind string) candidate {
	t.Helper()
	id, version := uuid.New(), uuid.New()
	execSeed(t, pool, `INSERT INTO reading_materials (id,slug,exam_type,difficulty,material_kind,created_by,updated_by) VALUES ($1,$2,$3,'intermediate',$4,$5,$5)`, id, id.String(), examType, kind, user)
	execSeed(t, pool, `INSERT INTO reading_material_versions (id,material_id,version_number,title,body,created_by,duration_minutes) VALUES ($1,$2,1,'Reading','Passage body',$3,60)`, version, id, user)
	if kind == "TEST" {
		for position := 1; position <= 3; position++ {
			passage := seedMockReading(t, pool, user, examType, "PASSAGE")
			execSeed(t, pool, `INSERT INTO reading_test_passages (test_material_version_id,position,passage_material_id,passage_material_version_id) VALUES ($1,$2,$3,$4)`, version, position, passage.materialID, passage.versionID)
			g := uuid.New()
			execSeed(t, pool, `INSERT INTO reading_question_groups (id,material_version_id,position,question_type,created_by) VALUES ($1,$2,1,'short_answer',$3)`, g, passage.versionID, user)
			count := 13
			if position == 3 {
				count = 14
			}
			for n := 1; n <= count; n++ {
				execSeed(t, pool, `INSERT INTO reading_questions (id,group_id,position,prompt,answer,created_by) VALUES ($1,$2,$3,'Question','{"value":"answer"}',$4)`, uuid.New(), g, n, user)
			}
		}
	}
	execSeed(t, pool, `UPDATE reading_materials SET current_version_id=$2,published_version_id=$2,status='PUBLISHED',published_at=CURRENT_TIMESTAMP,published_by=$3 WHERE id=$1`, id, version, user)
	return candidate{skill: "reading", materialID: id, versionID: version}
}

func seedMockJSONMaterial(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, skill, examType string) candidate {
	t.Helper()
	id, version := uuid.New(), uuid.New()
	switch skill {
	case "writing":
		execSeed(t, pool, `INSERT INTO writing_materials (id,slug) VALUES ($1,$2)`, id, id.String())
		execSeed(t, pool, `INSERT INTO writing_material_versions (id,material_id,version_number,exam_type,difficulty,title,tasks,created_by) VALUES ($1,$2,1,$3,'intermediate','Writing','[{"type":"task1"},{"type":"task2"}]',$4)`, version, id, examType, user)
		execSeed(t, pool, `UPDATE writing_materials SET published_version_id=$2,status='PUBLISHED',published_at=CURRENT_TIMESTAMP WHERE id=$1`, id, version)
	case "speaking":
		execSeed(t, pool, `INSERT INTO speaking_materials (id,slug) VALUES ($1,$2)`, id, id.String())
		execSeed(t, pool, `INSERT INTO speaking_material_versions (id,material_id,version_number,exam_type,difficulty,title,parts,created_by) VALUES ($1,$2,1,$3,'intermediate','Speaking','[{"type":"part1"},{"type":"part2"},{"type":"part3"}]',$4)`, version, id, examType, user)
		execSeed(t, pool, `UPDATE speaking_materials SET published_version_id=$2,status='PUBLISHED',published_at=CURRENT_TIMESTAMP WHERE id=$1`, id, version)
	}
	return candidate{skill: skill, materialID: id, versionID: version}
}

func seedMockBank(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) []candidate {
	t.Helper()
	return []candidate{seedMockListening(t, pool, user, 40), seedMockReading(t, pool, user, "academic", "TEST"), seedMockJSONMaterial(t, pool, user, "writing", "academic"), seedMockJSONMaterial(t, pool, user, "speaking", "general")}
}

func seedCompleted(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, item candidate, status string) {
	t.Helper()
	execSeed(t, pool, `INSERT INTO attempts (id,user_id,material_type,material_id,material_version_id,status,submitted_at,score,max_score) VALUES ($1,$2,$3,$4,$5,$6,CURRENT_TIMESTAMP,20,40)`, uuid.New(), user, item.skill, item.materialID, item.versionID, status)
}

func TestGeneratedMockCountsAndPrefersUnseen(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := mockUser(t, pool, "academic")
	repo := NewPostgresRepository(pool)
	bank := seedMockBank(t, pool, user)
	var completed []candidate
	for n := 0; n < 9; n++ {
		item := seedMockListening(t, pool, user, 40)
		completed = append(completed, item)
		seedCompleted(t, pool, user, item, "SUBMITTED")
		seedCompleted(t, pool, user, item, "SUBMITTED")
	}
	// A short exercise, a single passage, drafts, archived tests and the wrong
	// reading/writing format must never contribute to the full-exam bank.
	seedMockListening(t, pool, user, 10)
	seedMockReading(t, pool, user, "academic", "PASSAGE")
	seedMockReading(t, pool, user, "general", "TEST")
	seedMockJSONMaterial(t, pool, user, "writing", "general")
	archived := seedMockListening(t, pool, user, 40)
	execSeed(t, pool, `UPDATE listening_tests SET status='ARCHIVED' WHERE id=$1`, archived.materialID)
	draft := seedMockListening(t, pool, user, 40)
	execSeed(t, pool, `UPDATE listening_tests SET status='DRAFT' WHERE id=$1`, draft.materialID)
	other := mockUser(t, pool, "academic")
	seedCompleted(t, pool, other, bank[0], "SUBMITTED")
	seedCompleted(t, pool, user, bank[0], "ABANDONED")
	seedCompleted(t, pool, user, bank[2], "PROCESSING")
	overview, err := repo.MockOverview(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Ready || overview.Banks[0].Total != 10 || overview.Banks[0].Completed != 9 || overview.Banks[0].IsExhausted || !overview.Banks[2].IsExhausted {
		t.Fatalf("%+v", overview)
	}
	session, created, err := repo.GenerateSession(ctx, user, false)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	sections, err := repo.ListSessionSections(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for n, section := range sections {
		if section.Attempt.MaterialID != bank[n].materialID || section.Attempt.MaterialVersionID != bank[n].versionID {
			t.Fatalf("wrong draw: %+v", sections)
		}
	}
	seedCompleted(t, pool, user, bank[0], "SUBMITTED")
	overview, err = repo.MockOverview(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Banks[0].IsExhausted || overview.Banks[0].Completed != 10 {
		t.Fatalf("exhaustion: %+v", overview.Banks[0])
	}
	// All listening tests were completed; on restart it draws from the previous
	// tests, excluding the immediately previous full mock when possible.
	restarted, created, err := repo.GenerateSession(ctx, user, true)
	if err != nil || !created {
		t.Fatal(err)
	}
	sections, err = repo.ListSessionSections(ctx, restarted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sections[0].Attempt.MaterialID == bank[0].materialID {
		t.Fatal("immediate repeat despite alternatives")
	}
	seedMockListening(t, pool, user, 40)
	overview, err = repo.MockOverview(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Banks[0].IsExhausted || overview.Banks[0].Remaining != 1 || overview.Banks[0].Total != 11 {
		t.Fatalf("new content: %+v", overview.Banks[0])
	}
}

func TestGeneratedMockConcurrentStartSnapshotAndFailedRestart(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := mockUser(t, pool, "academic")
	repo := NewPostgresRepository(pool)
	bank := seedMockBank(t, pool, user)
	var wg sync.WaitGroup
	sessions := make(chan Session, 8)
	errs := make(chan error, 8)
	created := make(chan bool, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, c, e := repo.GenerateSession(ctx, user, false)
			sessions <- s
			created <- c
			errs <- e
		}()
	}
	wg.Wait()
	close(sessions)
	close(errs)
	close(created)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id uuid.UUID
	for s := range sessions {
		if id == uuid.Nil {
			id = s.ID
		}
		if s.ID != id {
			t.Fatal("duplicate concurrent session")
		}
	}
	count := 0
	for c := range created {
		if c {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("created %d exams", count)
	}
	var catalogs, attemptCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM full_mock_tests`).Scan(&catalogs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM attempts WHERE user_id=$1`, user).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if catalogs != 0 || attemptCount != 4 {
		t.Fatalf("catalogs=%d attempts=%d", catalogs, attemptCount)
	}
	// Archiving content does not alter the pinned exam or break its access guard.
	execSeed(t, pool, `UPDATE speaking_materials SET status='ARCHIVED' WHERE id=$1`, bank[3].materialID)
	s, c, err := repo.GenerateSession(ctx, user, false)
	if err != nil || c || s.ID != id {
		t.Fatal("resume changed")
	}
	if _, _, err := repo.GenerateSession(ctx, user, true); !errors.Is(err, ErrBankIncomplete) {
		t.Fatalf("missing bank: %v", err)
	}
	s, err = repo.GetSession(ctx, id)
	if err != nil || s.Status != SessionInProgress {
		t.Fatal("failed restart destroyed active exam")
	}
	sections, err := repo.ListSessionSections(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for n, sec := range sections {
		if sec.Attempt.MaterialVersionID != bank[n].versionID || sec.Attempt.Status != "IN_PROGRESS" {
			t.Fatal("pinned attempts changed")
		}
	}
	meta, err := repo.FindExamAttemptMeta(ctx, sections[0].Attempt.ID)
	if err != nil || meta == nil || meta.DurationMinutes != generatedDurationMinutes {
		t.Fatalf("generated exam guard: %+v %v", meta, err)
	}
}

func TestGeneratedMockRequiresProfileAndGeneralMaterials(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := testdb.User(t, pool)
	repo := NewPostgresRepository(pool)
	if _, _, err := repo.GenerateSession(ctx, user, false); !errors.Is(err, ErrExamTypeRequired) {
		t.Fatal(err)
	}
	execSeed(t, pool, `INSERT INTO user_profiles (user_id,display_name,exam_type) VALUES ($1,'Student','general')`, user)
	seedMockBank(t, pool, user)
	if _, _, err := repo.GenerateSession(ctx, user, false); !errors.Is(err, ErrBankIncomplete) {
		t.Fatal(err)
	}
	r := seedMockReading(t, pool, user, "general", "TEST")
	w := seedMockJSONMaterial(t, pool, user, "writing", "general")
	session, _, err := repo.GenerateSession(ctx, user, false)
	if err != nil {
		t.Fatal(err)
	}
	sections, err := repo.ListSessionSections(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.ExamType != "general" || sections[1].Attempt.MaterialID != r.materialID || sections[2].Attempt.MaterialID != w.materialID {
		t.Fatal("wrong exam format")
	}
}

func TestGeneratedMockExpiryAndPrivacy(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := mockUser(t, pool, "academic")
	repo := NewPostgresRepository(pool)
	seedMockBank(t, pool, user)
	providers := map[string]attempts.MaterialProvider{}
	for _, skill := range mockSkills {
		providers[skill] = expiryProvider{uuid.New()}
	}
	attemptService := attempts.NewService(attempts.NewPostgresRepository(pool), providers)
	svc := NewService(repo, attemptService)
	attemptService.SetExamGuard(svc)
	session, _, err := svc.StartGenerated(ctx, user, false)
	if err != nil {
		t.Fatal(err)
	}
	if session.MockTestID != uuid.Nil || session.MockTest.Title == "" || session.MockTest.DurationMinutes != 165 || len(session.Sections) != 4 || session.DeadlineAt.Before(time.Now()) {
		t.Fatalf("%+v", session)
	}
	other := mockUser(t, pool, "academic")
	if _, err := svc.GetSession(ctx, other, session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("foreign exam accessible")
	}
	if _, _, err := svc.GetSection(ctx, user, session.ID, 2); !errors.Is(err, ErrSectionLocked) {
		t.Fatal("future section unlocked")
	}
	execSeed(t, pool, `UPDATE full_mock_sessions SET started_at=CURRENT_TIMESTAMP-INTERVAL '4 hours' WHERE id=$1`, session.ID)
	if err := svc.ValidateAttemptAccess(ctx, user, session.Sections[0].Attempt.ID); !errors.Is(err, attempts.ErrExamDeadlineExceeded) {
		t.Fatal(err)
	}
	finished, err := svc.GetSession(ctx, user, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != SessionSubmitted || finished.OverallBand != nil {
		t.Fatal("expiry bypassed for generated exam")
	}
	next, newlyCreated, err := svc.StartGenerated(ctx, user, false)
	if err != nil || !newlyCreated || next.ID == session.ID {
		t.Fatalf("new=%v err=%v", newlyCreated, err)
	}
}
