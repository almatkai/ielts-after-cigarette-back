package fullmock

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func migrationSQL(t *testing.T, direction string) string {
	t.Helper()
	data, err := os.ReadFile("../../migrations/000030_dynamic_full_mock." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDynamicMigrationPreservesLegacyExamAndPinsMetadata(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := testdb.User(t, pool)
	repo := NewPostgresRepository(pool)
	execSeed(t, pool, migrationSQL(t, "down"))
	item, err := repo.Create(ctx, Test{ID: uuid.New(), Slug: "legacy-snapshot", ExamType: "academic", Title: "Legacy exam", DurationMinutes: 180,
		ListeningMaterialID: uuid.New(), ReadingMaterialID: uuid.New(), WritingMaterialID: uuid.New(), SpeakingMaterialID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	session := Session{ID: uuid.New(), MockTestID: item.ID, UserID: user}
	sections := []SessionSection{}
	for n, skill := range mockSkills {
		sections = append(sections, SessionSection{Position: n + 1, Skill: skill, Attempt: attempts.Attempt{ID: uuid.New(), UserID: user, MaterialType: skill, MaterialID: uuid.New(), MaterialVersionID: uuid.New(), Status: attempts.StatusInProgress}})
	}
	if err := repo.CreateSession(ctx, session, sections); err != nil {
		t.Fatal(err)
	}
	execSeed(t, pool, migrationSQL(t, "up"))
	execSeed(t, pool, `UPDATE full_mock_tests SET title='Changed later',duration_minutes=60 WHERE id=$1`, item.ID)
	svc := NewService(repo, attempts.NewService(attempts.NewPostgresRepository(pool), nil))
	saved, err := svc.GetSession(ctx, user, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.MockTest.Title != "Legacy exam" || saved.MockTest.DurationMinutes != 180 || len(saved.Sections) != 4 {
		t.Fatalf("lost legacy snapshot: %+v", saved)
	}
	meta, err := repo.FindExamAttemptMeta(ctx, sections[0].Attempt.ID)
	if err != nil || meta == nil || meta.DurationMinutes != 180 {
		t.Fatalf("legacy guard changed: %+v %v", meta, err)
	}
}

func TestDynamicRollbackRefusesToDeleteGeneratedHistory(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := mockUser(t, pool, "academic")
	seedMockBank(t, pool, user)
	repo := NewPostgresRepository(pool)
	session, _, err := repo.GenerateSession(ctx, user, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, migrationSQL(t, "down"))
	if err == nil || !strings.Contains(err.Error(), "generated sessions exist") {
		t.Fatalf("unsafe rollback accepted: %v", err)
	}
	saved, err := repo.GetSession(ctx, session.ID)
	if err != nil || saved.Status != SessionInProgress {
		t.Fatal("rollback lost exam")
	}
}
