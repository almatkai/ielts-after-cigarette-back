package attempts

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type cachedReviewProvider struct {
	MaterialProvider
	loads int
}

func (p *cachedReviewProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (GradingMaterial, error) {
	p.loads++
	return GradingMaterial{Questions: []GradingQuestion{{ID: questionChoiceID, Number: 1, Points: 1, Answer: map[string]any{"optionId": "A"}}}}, nil
}

func TestReviewCachesOnlyPinnedMaterialNotPrivateAnswers(t *testing.T) {
	repo := newStubRepository()
	id := uuid.New()
	repo.attempts[id] = Attempt{ID: id, UserID: testUserID, MaterialType: MaterialReading, MaterialID: testMaterialID, MaterialVersionID: testMaterialVersionID, Status: StatusSubmitted}
	repo.answers[id] = []Answer{{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "B"}}}
	provider := &cachedReviewProvider{}
	service := NewService(repo, map[string]MaterialProvider{MaterialReading: provider})
	first, err := service.Get(context.Background(), testUserID, id)
	if err != nil || len(first.Review) != 1 {
		t.Fatalf("first review: %+v / %v", first, err)
	}
	repo.answers[id] = []Answer{{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "C"}}}
	second, err := service.Get(context.Background(), testUserID, id)
	if err != nil || second.Review[0].Answer["optionId"] != "C" || provider.loads != 1 {
		t.Fatalf("cached student data or refetched material: %+v / %v loads=%d", second, err, provider.loads)
	}
	if _, err := service.Get(context.Background(), testOtherUser, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign review: %v", err)
	}
	repo.attempts[id] = Attempt{ID: id, UserID: testUserID, MaterialType: MaterialReading, MaterialID: testMaterialID, MaterialVersionID: uuid.New(), Status: StatusSubmitted}
	if _, err := service.Get(context.Background(), testUserID, id); err != nil || provider.loads != 2 {
		t.Fatalf("new pinned version reused old cache: %v loads=%d", err, provider.loads)
	}
}
