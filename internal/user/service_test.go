package user

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUpdateGoalValidatesBandAndPastDate(t *testing.T) {
	repository := &fakeRepository{profile: Profile{ID: uuid.New(), Timezone: "UTC"}}
	service := NewService(repository)
	service.now = func() time.Time {
		return time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	}

	band := 6.3
	date := "2026-07-25"
	examType := "academic"
	_, details, err := service.UpdateGoal(context.Background(), repository.profile.ID, GoalInput{
		TargetBand: &band,
		ExamDate:   &date,
		ExamType:   &examType,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details["targetBand"] == "" || details["examDate"] == "" {
		t.Fatalf("expected band and date errors, got %v", details)
	}
}

func TestUpdateGoalStoresValidGoal(t *testing.T) {
	repository := &fakeRepository{profile: Profile{ID: uuid.New(), Timezone: "UTC"}}
	service := NewService(repository)
	service.now = func() time.Time {
		return time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	}

	band := 7.5
	date := "2026-10-15"
	examType := "GENERAL"
	profile, details, err := service.UpdateGoal(context.Background(), repository.profile.ID, GoalInput{
		TargetBand: &band,
		ExamDate:   &date,
		ExamType:   &examType,
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("update goal failed: details=%v err=%v", details, err)
	}
	if profile.TargetBand == nil || *profile.TargetBand != band {
		t.Fatalf("target band was not stored: %+v", profile)
	}
	if profile.ExamType == nil || *profile.ExamType != "general" {
		t.Fatalf("exam type was not normalized: %+v", profile.ExamType)
	}
}

type fakeRepository struct {
	profile Profile
}

func (r *fakeRepository) Get(_ context.Context, _ uuid.UUID) (Profile, error) {
	return r.profile, nil
}

func (r *fakeRepository) UpdateProfile(_ context.Context, _ uuid.UUID, patch ProfilePatch) (Profile, error) {
	if patch.DisplayName != nil {
		r.profile.DisplayName = *patch.DisplayName
	}
	if patch.Timezone != nil {
		r.profile.Timezone = *patch.Timezone
	}
	return r.profile, nil
}

func (r *fakeRepository) UpdateGoal(
	_ context.Context,
	_ uuid.UUID,
	band float64,
	date time.Time,
	examType string,
) (Profile, error) {
	r.profile.TargetBand = &band
	dateValue := date.Format(time.DateOnly)
	r.profile.ExamDate = &dateValue
	r.profile.ExamType = &examType
	return r.profile, nil
}
