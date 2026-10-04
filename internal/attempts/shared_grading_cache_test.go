package attempts

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/google/uuid"
)

type sharedCacheProvider struct {
	MaterialProvider
	loads int
}

func (p *sharedCacheProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (GradingMaterial, error) {
	p.loads++
	return GradingMaterial{ExamType: "academic", Questions: []GradingQuestion{{ID: questionChoiceID, Answer: map[string]any{"optionId": "A"}, Points: 1}}}, nil
}

func TestSharedCacheSurvivesServiceRestartButNeverCachesStudentData(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	shared, err := cache.NewJSON(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	repo := newStubRepository()
	id, version := uuid.New(), uuid.New()
	repo.attempts[id] = Attempt{ID: id, UserID: testUserID, MaterialType: MaterialReading, MaterialID: testMaterialID, MaterialVersionID: version, Status: StatusSubmitted}
	repo.answers[id] = []Answer{{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "B"}}}
	provider := &sharedCacheProvider{}
	makeService := func() *Service {
		return NewService(repo, map[string]MaterialProvider{MaterialReading: provider}).WithGradingCache(shared)
	}
	if _, err := makeService().Get(context.Background(), testUserID, id); err != nil {
		t.Fatal(err)
	}
	repo.answers[id] = []Answer{{QuestionID: questionChoiceID, Answer: map[string]any{"optionId": "C"}}}
	otherReplica := makeService()
	detail, err := otherReplica.Get(context.Background(), testUserID, id)
	if err != nil || provider.loads != 1 || detail.Review[0].Answer["optionId"] != "C" {
		t.Fatalf("private data cached or L2 missed: loads=%d detail=%+v err=%v", provider.loads, detail, err)
	}
	if _, err := otherReplica.Get(context.Background(), testOtherUser, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign access: %v", err)
	}
	ref := MaterialRef{MaterialID: testMaterialID, VersionID: version}
	// Corrupt cached JSON must be a miss, never a successful zero-value grade.
	shared.PutMany(context.Background(), map[string]any{sharedGradingKey(MaterialReading, ref): "corrupt"}, time.Minute)
	if _, err := makeService().Get(context.Background(), testUserID, id); err != nil || provider.loads != 2 {
		t.Fatalf("corrupt fallback: %v loads=%d", err, provider.loads)
	}
	// Keep test artifacts short-lived without clearing any shared Redis data.
	shared.PutMany(context.Background(), map[string]any{sharedGradingKey(MaterialReading, ref): nil}, time.Millisecond)
}
