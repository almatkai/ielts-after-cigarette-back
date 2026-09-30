package attempts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBankDetailSharesContextsAndKeepsOnlyMistakes(t *testing.T) {
	reading := "a long passage shared by two questions"
	transcript := "a transcript shared by two questions"
	detail := Detail{Attempt: Attempt{ID: uuid.New(), MaterialType: MaterialReading}, Review: []ReviewAnswer{
		{QuestionID: uuid.New(), PassageBody: reading, IsCorrect: true},
		{QuestionID: uuid.New(), PassageBody: reading, Answer: map[string]any{"value": "wrong"}, CorrectAnswer: map[string]any{"accepted": []string{"right"}}, Hint: "hint"},
		{QuestionID: uuid.New(), PassageBody: reading},
		{QuestionID: uuid.New(), Transcript: transcript},
		{QuestionID: uuid.New(), Transcript: transcript},
	}}
	result := bankDetail(detail)
	if result.Attempt.ID != detail.ID || len(result.Review) != 4 || len(result.Contexts) != 2 {
		t.Fatalf("unexpected detail: %+v", result)
	}
	if result.Review[0].ContextIndex != result.Review[1].ContextIndex || result.Review[2].ContextIndex != result.Review[3].ContextIndex {
		t.Fatal("questions do not share contexts")
	}
	if result.Review[0].Answer["value"] != "wrong" || result.Review[0].Hint != "hint" || result.Review[0].CorrectAnswer == nil {
		t.Fatal("lost question review data")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), reading) != 1 || strings.Count(string(encoded), transcript) != 1 {
		t.Fatalf("contexts duplicated in JSON: %s", encoded)
	}
	if detail.Review[1].PassageBody != reading {
		t.Fatal("changed the existing attempt detail")
	}
	if empty := bankDetail(Detail{}); empty.Review == nil || empty.Contexts == nil {
		t.Fatal("empty lists must serialize as arrays")
	}
}

func TestMistakeDetailKeepsOwnershipCheck(t *testing.T) {
	repo := newStubRepository()
	user, other := uuid.New(), uuid.New()
	attempt, err := repo.Create(context.Background(), Attempt{ID: uuid.New(), UserID: user, MaterialType: MaterialReading})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, nil)
	if _, err := service.MistakeDetail(context.Background(), other, attempt.ID); err != ErrNotFound {
		t.Fatalf("foreign attempt error = %v", err)
	}
}

func TestMistakeAttemptsRejectInvalidQueryBeforeLoading(t *testing.T) {
	handler := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 1024)
	for _, query := range []string{"materialType=all", "page=0", "page=-1", "page=1.5", "page=100001", "page=999999999999999999999", "limit=0", "limit=51"} {
		t.Run(query, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.MistakeAttempts(response, httptest.NewRequest("GET", "/attempts/mistakes/attempts?"+query, nil))
			if response.Code != 422 {
				t.Fatalf("status = %d, want 422", response.Code)
			}
		})
	}
}

// A list-only repository ensures the bank never calls ListByUser, loads
// answers or builds historical grading materials to produce the first page.
type bankListRepository struct {
	Repository
	user   uuid.UUID
	called bool
}

func (r *bankListRepository) ListMistakeAttempts(_ context.Context, user uuid.UUID, skill string, page, limit int) (MistakeAttemptsPage, error) {
	r.called = true
	if user != r.user || skill != MaterialListening || page != 2 || limit != 12 {
		panic("lost list filters")
	}
	return MistakeAttemptsPage{Items: []MistakeAttempt{}, Page: page}, nil
}

func TestMistakeAttemptsOnlyLoadsRequestedPage(t *testing.T) {
	repo := &bankListRepository{user: uuid.New()}
	service := NewService(repo, nil)
	result, err := service.MistakeAttempts(context.Background(), repo.user, MaterialListening, 2, 12)
	if err != nil || !repo.called || result.Page != 2 {
		t.Fatalf("page = %+v, error = %v", result, err)
	}
}
