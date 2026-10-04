package listening

import (
	"context"
	"fmt"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
)

func TestBatchImportPreservesFortyQuestionsAndRollsBackFailure(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	actor := testdb.User(t, pool)
	input := SaveInput{Slug: "batch-import", ExamType: "academic", Title: "Batch import", DurationMinutes: 40}
	for p := 0; p < 4; p++ {
		questions := make([]Question, 10)
		for q := range questions {
			questions[q] = gradingQuestion(q+1, p*10+q+1, fmt.Sprintf("Question %d", p*10+q+1), "B")
		}
		part := gradingPart(fmt.Sprintf("Part %d", p+1), "Transcript", questions)
		part.Position = p + 1
		input.Parts = append(input.Parts, part)
	}
	// Fail near the end of the batch, after earlier parts/questions executed.
	input.Parts[3].Groups[0].Questions[9].Points = 11
	if _, err := repo.Create(ctx, actor, input); err == nil {
		t.Fatal("expected question CHECK failure")
	}
	for _, table := range []string{"listening_tests", "listening_test_versions", "listening_parts", "listening_question_groups", "listening_questions"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained partial import: %d / %v", table, count, err)
		}
	}
	input.Parts[3].Groups[0].Questions[9].Points = 1
	created, err := repo.Create(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Get(ctx, created.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Parts) != 4 {
		t.Fatalf("parts = %d", len(loaded.Parts))
	}
	for p, part := range loaded.Parts {
		if part.Position != p+1 || len(part.Groups) != 1 || len(part.Groups[0].Questions) != 10 {
			t.Fatalf("part shape changed: %+v", part)
		}
		for q, question := range part.Groups[0].Questions {
			if question.Number != p*10+q+1 || question.Points != 1 || question.Answer["optionId"] != "B" {
				t.Fatalf("question changed: %+v", question)
			}
		}
	}
}
