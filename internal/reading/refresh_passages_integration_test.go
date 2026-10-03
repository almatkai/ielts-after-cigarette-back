package reading

import (
	"context"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
)

func saveInputForRefreshTest(m Material) SaveInput {
	return SaveInput{
		Kind: m.Kind, Slug: m.Slug, ExamType: m.ExamType, Difficulty: m.Difficulty,
		Title: m.Title, Description: m.Description, Body: m.Body,
		DurationMinutes: m.DurationMinutes, SourceTitle: m.SourceTitle, SourceURL: m.SourceURL,
		QuestionGroups: m.QuestionGroups, Revision: m.Revision,
	}
}

// Refresh only opts the newly saved test into current passage versions. Published
// tests and attempts referencing a previous test version must not be changed.
func TestUpdateTestRefreshPassagesPreservesPinnedVersions(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repository := NewPostgresRepository(pool)
	service := NewService(repository)
	actor := testdb.User(t, pool)
	seeded := seedReadingTest(t, ctx, service, actor)
	published, details, err := service.Publish(ctx, seeded.ID, actor, seeded.Revision)
	if err != nil || len(details) != 0 {
		t.Fatalf("publish: %v %v", err, details)
	}
	passage := seeded.Passages[0]
	input := saveInputForRefreshTest(passage)
	input.QuestionGroups[0].Questions[0].Explanation = "A newly added explanation."
	input.QuestionGroups[0].Questions[0].Content = map[string]any{"hint": "Read the opening sentence."}
	updatedPassage, details, err := service.Update(ctx, passage.ID, actor, input)
	if err != nil || len(details) != 0 {
		t.Fatalf("update passage: %v %v", err, details)
	}
	if updatedPassage.CurrentVersionNumber == passage.CurrentVersionNumber {
		t.Fatal("passage update did not create a new version")
	}

	// Normal updates must retain the previous behavior: no implicit refresh.
	normal, details, err := service.Update(ctx, seeded.ID, actor, saveInputForRefreshTest(published))
	if err != nil || len(details) != 0 {
		t.Fatalf("normal update: %v %v", err, details)
	}
	if got := normal.Passages[0].QuestionGroups[0].Questions[0].Explanation; got != "" {
		t.Fatalf("normal update unexpectedly refreshed explanation: %q", got)
	}
	input = saveInputForRefreshTest(normal)
	input.RefreshPassages = true
	refreshed, details, err := service.Update(ctx, seeded.ID, actor, input)
	if err != nil || len(details) != 0 {
		t.Fatalf("refresh update: %v %v", err, details)
	}
	if got := refreshed.Passages[0].QuestionGroups[0].Questions[0].Explanation; got != "A newly added explanation." {
		t.Fatalf("refreshed explanation = %q, want the current passage explanation", got)
	}
	if got := refreshed.Passages[0].QuestionGroups[0].Questions[0].Content["hint"]; got != "Read the opening sentence." {
		t.Fatalf("refreshed hint = %v", got)
	}
	if len(refreshed.Passages) != len(seeded.Passages) || refreshed.Passages[1].ID != seeded.Passages[1].ID {
		t.Fatal("refresh changed the passage order or identities")
	}
	if refreshed.CurrentVersionID == normal.CurrentVersionID {
		t.Fatal("refresh overwrote an existing test version")
	}

	for _, version := range []Material{seeded, normal} {
		old, err := service.GetVersion(ctx, version.ID, version.CurrentVersionID)
		if err != nil {
			t.Fatal(err)
		}
		if got := old.Passages[0].QuestionGroups[0].Questions[0].Explanation; got != "" {
			t.Fatalf("pinned test version was mutated: %q", got)
		}
		ref := VersionRef{MaterialID: version.ID, VersionID: version.CurrentVersionID}
		grading, err := service.GradingStructures(ctx, []VersionRef{ref})
		if err != nil {
			t.Fatal(err)
		}
		if got := grading[ref].Passages[0].QuestionGroups[0].Questions[0].Explanation; got != "" {
			t.Fatalf("grading snapshot was mutated: %q", got)
		}
	}
	public, err := repository.GetPublished(ctx, seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := public.Passages[0].QuestionGroups[0].Questions[0].Explanation; got != "" {
		t.Fatalf("published test changed without publication: %q", got)
	}
}

func TestRefreshPassagesValidation(t *testing.T) {
	input := SaveInput{Kind: KindPassage, RefreshPassages: true}
	if validateInput(input, true)["refreshPassages"] == "" {
		t.Fatal("passage updates must reject refreshPassages")
	}
	input.Kind = KindTest
	if validateInput(input, false)["refreshPassages"] == "" {
		t.Fatal("creates must reject refreshPassages")
	}
	if validateInput(input, true)["refreshPassages"] != "" {
		t.Fatal("test updates must accept refreshPassages")
	}
}
