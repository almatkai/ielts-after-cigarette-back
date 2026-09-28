package reading

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func passageBody(label string) string {
	return strings.Repeat(label+" passage body text. ", 4)
}

// seedReadingTest creates a reading TEST with two passages that each carry their
// own question group, which is the two-level structure the grading loader has
// to expand.
func seedReadingTest(t *testing.T, ctx context.Context, service *Service, actor uuid.UUID) Material {
	t.Helper()
	items, details, err := service.BulkCreate(ctx, actor, BulkCreateInput{
		Title:           "Reading test for grading",
		DurationMinutes: 60,
		Passages: []SaveInput{
			{
				Slug: "grading-passage-one", ExamType: "academic", Difficulty: "intermediate",
				Title: "Passage one", Body: passageBody("first"),
				QuestionGroups: []QuestionGroup{{
					Position: 1, Type: QuestionTrueFalseNotGiven, Instructions: "True, false or not given",
					Questions: []Question{
						{Position: 1, Prompt: "First claim.", Points: 1, Answer: map[string]any{"value": "TRUE"}},
						{Position: 2, Prompt: "Second claim.", Points: 1, Answer: map[string]any{"value": "FALSE"}},
					},
				}},
			},
			{
				Slug: "grading-passage-two", ExamType: "academic", Difficulty: "intermediate",
				Title: "Passage two", Body: passageBody("second"),
				QuestionGroups: []QuestionGroup{{
					Position: 1, Type: QuestionShortAnswer, Instructions: "Answer with a word",
					Questions: []Question{
						{Position: 1, Prompt: "Which word?", Points: 1, Answer: map[string]any{"accepted": []any{"alpha"}}},
					},
				}},
			},
		},
	})
	if err != nil || len(details) > 0 {
		t.Fatalf("seed reading test: %v %v", err, details)
	}
	if len(items) != 1 {
		t.Fatalf("seeded materials = %d, want the test material only", len(items))
	}
	return items[0]
}

// TestGradingStructuresExpandsTestPassages covers the loader the mistakes page
// uses: a reading test has to come back with the structure of every passage it
// links, in both directions of the nesting level.
func TestGradingStructuresExpandsTestPassages(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	service := NewService(NewPostgresRepository(pool))
	actor := testdb.User(t, pool)
	seeded := seedReadingTest(t, ctx, service, actor)
	ref := VersionRef{MaterialID: seeded.ID, VersionID: seeded.CurrentVersionID}

	loaded, err := service.GradingStructures(ctx, []VersionRef{ref})
	if err != nil {
		t.Fatalf("grading structures: %v", err)
	}
	material, ok := loaded[ref]
	if !ok {
		t.Fatalf("version %s is missing from the %d loaded materials", ref.VersionID, len(loaded))
	}
	if len(material.QuestionGroups) != 0 {
		t.Fatalf("test groups = %d, want the questions of the passages", len(material.QuestionGroups))
	}
	if len(material.Passages) != 2 {
		t.Fatalf("passages = %d, want 2", len(material.Passages))
	}
	for index, passage := range material.Passages {
		if passage.ID == uuid.Nil || passage.CurrentVersionID == uuid.Nil {
			t.Fatalf("passage %d came back without its identity: %+v", index, passage)
		}
		if len(passage.QuestionGroups) != 1 {
			t.Fatalf("passage %d groups = %d, want 1", index, len(passage.QuestionGroups))
		}
		if passage.Body == "" {
			t.Fatalf("passage %d came back without its body", index)
		}
	}
	first := material.Passages[0].QuestionGroups[0]
	if first.Type != QuestionTrueFalseNotGiven || len(first.Questions) != 2 {
		t.Fatalf("first passage group = %+v, want the seeded questions", first)
	}
	if first.Questions[0].Answer["value"] != "TRUE" {
		t.Fatalf("first passage answer = %v, want the seeded answer", first.Questions[0].Answer)
	}
	second := material.Passages[1].QuestionGroups[0]
	if second.Type != QuestionShortAnswer || len(second.Questions) != 1 {
		t.Fatalf("second passage group = %+v, want the seeded question", second)
	}

	if _, err := service.GradingStructures(ctx, []VersionRef{{MaterialID: uuid.New(), VersionID: uuid.New()}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version error = %v, want %v", err, ErrNotFound)
	}
	if loaded, err := service.GradingStructures(ctx, nil); err != nil || len(loaded) != 0 {
		t.Fatalf("empty refs = %v/%v, want an empty result", loaded, err)
	}
}
