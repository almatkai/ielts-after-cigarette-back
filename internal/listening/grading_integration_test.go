package listening

import (
	"context"
	"errors"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func gradingQuestion(position, number int, prompt, optionID string) Question {
	return Question{
		Position: position, Number: number, Prompt: prompt, Points: 1,
		Content: map[string]any{"hint": "hint " + prompt},
		Answer:  map[string]any{"optionId": optionID},
	}
}

// seedListeningTest creates a DRAFT test with the given parts and returns the
// material and the version every attempt of the test would be pinned to.
func seedListeningTest(t *testing.T, ctx context.Context, repository *PostgresRepository, actor uuid.UUID, slug string, parts []Part) (uuid.UUID, uuid.UUID) {
	t.Helper()
	test, err := repository.Create(ctx, actor, SaveInput{
		Slug: slug, ExamType: "academic", Title: "Grading test",
		Description: "seeded for grading", DurationMinutes: 40, Parts: parts,
	})
	if err != nil {
		t.Fatalf("seed listening test: %v", err)
	}
	var versionID uuid.UUID
	if err := repository.pool.QueryRow(ctx, `SELECT current_version_id FROM listening_tests WHERE id=$1`, test.ID).Scan(&versionID); err != nil {
		t.Fatalf("read seeded version: %v", err)
	}
	return test.ID, versionID
}

func gradingPart(title, transcript string, questions []Question) Part {
	return Part{
		Title: title, Transcript: transcript,
		Groups: []QuestionGroup{{
			Position: 1, Type: TypeMultipleChoice, Instructions: "Choose one letter",
			Config: map[string]any{}, Questions: questions,
		}},
	}
}

// TestGradingStructuresLoadsManyVersions covers the bulk load the mistakes page
// uses: two tests of a different shape have to come back with their own parts,
// groups and questions, not mixed up with each other.
func TestGradingStructuresLoadsManyVersions(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repository := NewPostgresRepository(pool)
	actor := testdb.User(t, pool)

	firstID, firstVersion := seedListeningTest(t, ctx, repository, actor, "grading-first", []Part{
		gradingPart("Part one", "first transcript", []Question{
			gradingQuestion(1, 1, "First one", "A"),
			gradingQuestion(2, 2, "First two", "B"),
		}),
	})
	secondID, secondVersion := seedListeningTest(t, ctx, repository, actor, "grading-second", []Part{
		gradingPart("Part one", "second transcript", []Question{
			gradingQuestion(1, 21, "Second one", "C"),
		}),
		gradingPart("Part two", "second part transcript", []Question{
			gradingQuestion(1, 22, "Second two", "A"),
			gradingQuestion(2, 23, "Second three", "B"),
			gradingQuestion(3, 24, "Second four", "C"),
		}),
	})
	refs := []VersionRef{
		{MaterialID: firstID, VersionID: firstVersion},
		{MaterialID: secondID, VersionID: secondVersion},
	}

	loaded, err := repository.GradingStructures(ctx, refs)
	if err != nil {
		t.Fatalf("grading structures: %v", err)
	}
	if len(loaded) != len(refs) {
		t.Fatalf("loaded %d versions, want %d", len(loaded), len(refs))
	}
	first, ok := loaded[refs[0]]
	if !ok {
		t.Fatalf("first version %s is missing", firstVersion)
	}
	if first.ID != firstID || first.ExamType != "academic" {
		t.Fatalf("first test = %s/%s, want %s/academic", first.ID, first.ExamType, firstID)
	}
	if len(first.Parts) != 1 {
		t.Fatalf("first parts = %d, want 1", len(first.Parts))
	}
	if first.Parts[0].Transcript != "first transcript" {
		t.Fatalf("first transcript = %q", first.Parts[0].Transcript)
	}
	if got := len(first.Parts[0].Groups[0].Questions); got != 2 {
		t.Fatalf("first questions = %d, want 2", got)
	}
	if first.Parts[0].Groups[0].Questions[0].Content["hint"] != "hint First one" {
		t.Fatalf("first question content = %v, want the decoded hint", first.Parts[0].Groups[0].Questions[0].Content)
	}

	second, ok := loaded[refs[1]]
	if !ok {
		t.Fatalf("second version %s is missing", secondVersion)
	}
	if len(second.Parts) != 2 {
		t.Fatalf("second parts = %d, want 2", len(second.Parts))
	}
	if second.Parts[0].Groups[0].Questions[0].Number != 21 {
		t.Fatalf("second first question = %d, want 21", second.Parts[0].Groups[0].Questions[0].Number)
	}
	if got := len(second.Parts[1].Groups[0].Questions); got != 3 {
		t.Fatalf("second part questions = %d, want 3", got)
	}
	if second.Parts[1].Transcript != "second part transcript" {
		t.Fatalf("second part transcript = %q", second.Parts[1].Transcript)
	}

	if _, err := repository.GradingStructures(ctx, []VersionRef{{MaterialID: uuid.New(), VersionID: uuid.New()}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version error = %v, want %v", err, ErrNotFound)
	}
	if loaded, err := repository.GradingStructures(ctx, nil); err != nil || len(loaded) != 0 {
		t.Fatalf("empty refs = %v/%v, want an empty result", loaded, err)
	}
}

// TestGetVersionKeepsPartsOfOneVersion checks the single-version path shares the
// bulk query without leaking the parts of other versions into it.
func TestGetVersionKeepsPartsOfOneVersion(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repository := NewPostgresRepository(pool)
	actor := testdb.User(t, pool)
	firstID, firstVersion := seedListeningTest(t, ctx, repository, actor, "grading-single-first", []Part{
		gradingPart("Part one", "first transcript", []Question{
			gradingQuestion(1, 1, "First one", "A"),
		}),
	})
	seedListeningTest(t, ctx, repository, actor, "grading-single-second", []Part{
		gradingPart("Part one", "other transcript", []Question{
			gradingQuestion(1, 1, "Other one", "A"),
		}),
	})

	test, err := repository.GetVersion(ctx, firstID, firstVersion)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if len(test.Parts) != 1 || len(test.Parts[0].Groups[0].Questions) != 1 {
		t.Fatalf("parts = %+v, want only the parts of the requested version", test.Parts)
	}
	if test.Parts[0].Transcript != "first transcript" {
		t.Fatalf("transcript = %q, want the transcript of the requested version", test.Parts[0].Transcript)
	}
}
