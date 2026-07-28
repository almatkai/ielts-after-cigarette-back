package user

import (
	"context"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Profile, error) {
	return s.repository.Get(ctx, userID)
}

func (s *Service) UpdateProfile(
	ctx context.Context,
	userID uuid.UUID,
	patch ProfilePatch,
) (Profile, map[string]string, error) {
	details := map[string]string{}
	if patch.DisplayName != nil {
		value := strings.TrimSpace(*patch.DisplayName)
		patch.DisplayName = &value
		length := utf8.RuneCountInString(value)
		if length < 2 || length > 100 {
			details["displayName"] = "must contain between 2 and 100 characters"
		}
	}
	if patch.Timezone != nil {
		value := strings.TrimSpace(*patch.Timezone)
		patch.Timezone = &value
		if len(value) == 0 || len(value) > 64 {
			details["timezone"] = "must contain between 1 and 64 characters"
		} else if _, err := time.LoadLocation(value); err != nil {
			details["timezone"] = "must be a valid IANA timezone"
		}
	}
	if patch.DisplayName == nil && patch.Timezone == nil {
		details["body"] = "must contain displayName or timezone"
	}
	if len(details) > 0 {
		return Profile{}, details, nil
	}
	profile, err := s.repository.UpdateProfile(ctx, userID, patch)
	return profile, nil, err
}

func (s *Service) UpdateGoal(
	ctx context.Context,
	userID uuid.UUID,
	input GoalInput,
) (Profile, map[string]string, error) {
	details := map[string]string{}
	if input.TargetBand == nil {
		details["targetBand"] = "is required"
	} else if !validBand(*input.TargetBand) {
		details["targetBand"] = "must be between 0 and 9 in increments of 0.5"
	}

	var examDate time.Time
	if input.ExamDate == nil {
		details["examDate"] = "is required"
	} else {
		value := strings.TrimSpace(*input.ExamDate)
		var err error
		examDate, err = time.Parse(time.DateOnly, value)
		if err != nil {
			details["examDate"] = "must use YYYY-MM-DD format"
		}
	}

	examType := ""
	if input.ExamType == nil {
		details["examType"] = "is required"
	} else {
		examType = strings.ToLower(strings.TrimSpace(*input.ExamType))
		if examType != "academic" && examType != "general" {
			details["examType"] = "must be academic or general"
		}
	}

	if !examDate.IsZero() {
		profile, err := s.repository.Get(ctx, userID)
		if err != nil {
			return Profile{}, nil, err
		}
		location, err := time.LoadLocation(profile.Timezone)
		if err != nil {
			location = time.UTC
		}
		now := s.now().In(location)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		candidate := time.Date(examDate.Year(), examDate.Month(), examDate.Day(), 0, 0, 0, 0, location)
		if candidate.Before(today) {
			details["examDate"] = "must not be in the past"
		}
	}
	if len(details) > 0 {
		return Profile{}, details, nil
	}
	profile, err := s.repository.UpdateGoal(ctx, userID, *input.TargetBand, examDate, examType)
	return profile, nil, err
}

func validBand(value float64) bool {
	return value >= 0 && value <= 9 && math.Abs(value*2-math.Round(value*2)) < 1e-9
}
