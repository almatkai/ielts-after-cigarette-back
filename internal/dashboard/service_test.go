package dashboard

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestDashboardForNewUserHasStableEmptyCollections(t *testing.T) {
	service := NewService(stubRepository{})
	response, err := service.Get(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	if response.TodayPlan == nil || len(response.TodayPlan) != 0 {
		t.Fatalf("today plan must be an empty JSON array: %#v", response.TodayPlan)
	}
	if len(response.SkillProgress) != 4 {
		t.Fatalf("expected four skill rows, got %d", len(response.SkillProgress))
	}
	if response.RecommendedAction.Target != "/dashboard/practice" {
		t.Fatalf("unexpected frontend target: %s", response.RecommendedAction.Target)
	}
}

type stubRepository struct{}

func (stubRepository) Get(context.Context, uuid.UUID) (Response, error) {
	return Response{SkillProgress: []SkillProgress{
		{Skill: "listening"},
		{Skill: "reading"},
		{Skill: "writing"},
		{Skill: "speaking"},
	}}, nil
}
