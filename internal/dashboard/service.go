package dashboard

import (
	"context"

	"github.com/google/uuid"
)

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Response, error) {
	response, err := s.repository.Get(ctx, userID)
	if err != nil {
		return Response{}, err
	}
	response.RecommendedAction = RecommendedAction{
		Type:        "START_DIAGNOSTIC",
		Title:       "Определите стартовый уровень",
		Description: "Короткая диагностика поможет подобрать подходящую сложность.",
		Target:      "/dashboard/practice",
	}
	if response.TodayPlan == nil {
		response.TodayPlan = []TodayPlanItem{}
	}
	if response.SkillProgress == nil {
		response.SkillProgress = []SkillProgress{}
	}
	return response, nil
}
