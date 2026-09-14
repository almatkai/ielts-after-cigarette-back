package reading

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type repositoryStub struct {
	created     SaveInput
	createdMany []SaveInput
	updated     SaveInput
}

func (r *repositoryStub) List(context.Context) ([]Material, error) { return nil, nil }
func (r *repositoryStub) Get(context.Context, uuid.UUID) (Material, error) {
	return Material{}, nil
}
func (r *repositoryStub) ListPublished(context.Context) ([]MaterialSummary, error) {
	return nil, nil
}
func (r *repositoryStub) GetPublished(context.Context, uuid.UUID) (Material, error) {
	return Material{}, nil
}
func (r *repositoryStub) GetVersion(context.Context, uuid.UUID, uuid.UUID) (Material, error) {
	return Material{}, nil
}
func (r *repositoryStub) PublishedVersionID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (r *repositoryStub) Create(_ context.Context, _ uuid.UUID, input SaveInput) (Material, error) {
	r.created = input
	return Material{Slug: input.Slug}, nil
}
func (r *repositoryStub) CreateMany(_ context.Context, _ uuid.UUID, inputs []SaveInput) ([]Material, error) {
	r.createdMany = inputs
	items := make([]Material, len(inputs))
	for index, input := range inputs {
		items[index] = Material{Title: input.Title}
	}
	return items, nil
}

func TestBulkCreateWrapsPassagesInOneVersionedTest(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service := NewService(repository)
	body := "A sufficiently long synthetic passage used to verify the complete reading test workflow."
	items, details, err := service.BulkCreate(context.Background(), uuid.New(), BulkCreateInput{
		Title: "Complete Reading", DurationMinutes: 60,
		Passages: []SaveInput{
			{ExamType: "academic", Difficulty: "intermediate", Title: "Passage one", Body: body},
			{ExamType: "academic", Difficulty: "intermediate", Title: "Passage two", Body: body},
		},
	})
	if err != nil || len(details) > 0 || len(items) != 1 {
		t.Fatalf("items=%#v details=%v err=%v", items, details, err)
	}
	if len(repository.createdMany) != 3 || repository.createdMany[0].Kind != KindTest || repository.createdMany[1].Kind != KindPassage {
		t.Fatalf("created inputs = %#v", repository.createdMany)
	}
	if repository.createdMany[0].DurationMinutes == nil || *repository.createdMany[0].DurationMinutes != 60 {
		t.Fatalf("test duration = %#v", repository.createdMany[0].DurationMinutes)
	}
}
func (r *repositoryStub) Update(_ context.Context, _, _ uuid.UUID, input SaveInput) (Material, error) {
	r.updated = input
	return Material{Revision: input.Revision + 1}, nil
}
func (r *repositoryStub) Publish(context.Context, uuid.UUID, uuid.UUID, int64) (Material, error) {
	return Material{Status: StatusPublished}, nil
}
func (r *repositoryStub) Archive(context.Context, uuid.UUID, uuid.UUID, int64) (Material, error) {
	return Material{Status: StatusArchived}, nil
}

func TestCreateMaterialNormalizesAndValidates(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service := NewService(repository)
	body := "A sufficiently long reading passage used to validate the first content workflow."

	_, details, err := service.Create(context.Background(), uuid.New(), SaveInput{
		Slug:       "  First-Passage ",
		ExamType:   " ACADEMIC ",
		Difficulty: " Intermediate ",
		Title:      "  First passage  ",
		Body:       body,
	})
	if err != nil || len(details) > 0 {
		t.Fatalf("Create() details = %v, error = %v", details, err)
	}
	if repository.created.Slug != "first-passage" || repository.created.ExamType != "academic" {
		t.Fatalf("normalized input = %#v", repository.created)
	}
}

func TestUpdateMaterialRequiresRevision(t *testing.T) {
	t.Parallel()
	service := NewService(&repositoryStub{})

	_, details, err := service.Update(context.Background(), uuid.New(), uuid.New(), SaveInput{
		Slug:       "first-passage",
		ExamType:   "academic",
		Difficulty: "intermediate",
		Title:      "First passage",
		Body:       "A sufficiently long reading passage used to validate the first content workflow.",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if details["revision"] == "" {
		t.Fatalf("details = %v, want revision error", details)
	}
}
