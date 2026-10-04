package reading

import (
	"context"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
)

func TestBatchQuestionImportRollsBackLateFailure(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	repo := NewPostgresRepository(pool)
	seed := seedReadingTest(t, ctx, NewService(repo), actor)
	input := saveInputForRefreshTest(seed.Passages[0])
	input.Slug = "batch-rollback"
	input.QuestionGroups = append([]QuestionGroup(nil), input.QuestionGroups...)
	input.QuestionGroups[0].Questions = append([]Question(nil), input.QuestionGroups[0].Questions...)
	last := len(input.QuestionGroups[0].Questions) - 1
	input.QuestionGroups[0].Questions[last].Points = 11
	var before, after int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reading_questions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, actor, input); err == nil {
		t.Fatal("expected question CHECK failure")
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reading_questions`).Scan(&after); err != nil || after != before {
		t.Fatalf("partial questions retained: %d -> %d / %v", before, after, err)
	}
	var materials int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reading_materials WHERE slug=$1`, input.Slug).Scan(&materials); err != nil || materials != 0 {
		t.Fatalf("failed material persisted: %d / %v", materials, err)
	}
	input.QuestionGroups[0].Questions[last].Points = 1
	created, err := repo.Create(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetVersion(ctx, created.ID, created.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.QuestionGroups) != len(input.QuestionGroups) || len(loaded.QuestionGroups[0].Questions) != last+1 {
		t.Fatal("batch lost groups/questions")
	}
}
