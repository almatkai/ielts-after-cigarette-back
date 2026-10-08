package fullmock

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/google/uuid"
)

func TestSectionReviewsRemainPrivateUntilEntireMockCompletes(t *testing.T) {
	for _, role := range []string{"GUEST", "STUDENT"} {
		t.Run(role, func(t *testing.T) {
			service, repo, attemptService, attemptRepo, _ := setupTestFullMockService(t)
			owner, sessionID := uuid.New(), uuid.New()
			ctx := auth.WithUser(context.Background(), owner, role)
			band, score := 7.5, 30
			for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
				id := uuid.New()
				stored := attempts.Attempt{ID: id, UserID: owner, MaterialType: skill, Status: attempts.StatusSubmitted, Band: &band, Score: &score, MaxScore: &score}
				attemptRepo.attempts[id] = stored
				correct := true
				attemptRepo.answers[id] = []attempts.Answer{{QuestionID: uuid.New(), Answer: map[string]any{"value": "PRIVATE_ANSWER"}, IsCorrect: &correct}}
				repo.attemptMeta[id] = &ExamAttemptMeta{SessionID: sessionID, UserID: owner, SessionStatus: SessionInProgress}
				detail, err := attemptService.Get(ctx, owner, id)
				if err != nil {
					t.Fatal(err)
				}
				response := attempts.DetailForViewer(ctx, detail)
				data, _ := json.Marshal(response)
				if !response.ReviewLocked || response.FullMockSessionID == nil || *response.FullMockSessionID != sessionID || response.Band != nil || response.Score != nil || response.MaxScore != nil || len(response.Answers) > 0 || len(response.Review) > 0 || response.GuestPreview != nil || response.WritingEvaluation != nil || response.SpeakingEvaluation != nil || strings.Contains(string(data), "PRIVATE_ANSWER") {
					t.Fatalf("%s leaks a premature review: %s", skill, data)
				}
				projected, err := attemptService.ReviewAttempt(ctx, owner, stored)
				if err != nil || projected.Band != nil || !projected.ReviewLocked {
					t.Fatalf("submit projection leaks grade: %+v %v", projected, err)
				}
				if attemptRepo.attempts[id].Band == nil || *attemptRepo.attempts[id].Band != band {
					t.Fatal("stored grade was changed")
				}
				if _, _, err := service.ReviewAccess(ctx, uuid.New(), id); !errors.Is(err, attempts.ErrNotFound) {
					t.Fatalf("ownership guard: %v", err)
				}
				repo.attemptMeta[id].SessionStatus = SessionSubmitted
				projected, err = attemptService.ReviewAttempt(ctx, owner, stored)
				if err != nil || projected.ReviewLocked || projected.Band == nil {
					t.Fatalf("completed mock did not unlock: %+v %v", projected, err)
				}
				if skill == "reading" || skill == "listening" {
					detail, err = attemptService.Get(ctx, owner, id)
					if err != nil || detail.ReviewLocked || detail.Band == nil {
						t.Fatalf("completed detail did not unlock: %+v %v", detail, err)
					}
					response = attempts.DetailForViewer(ctx, detail)
					if role == "GUEST" && (response.GuestPreview == nil || response.Band != nil) {
						t.Fatal("completion bypassed guest quota")
					}
					if role == "STUDENT" && response.Band == nil {
						t.Fatal("completion still hides account grade")
					}
				}
			}
		})
	}
}

func TestRunningMockProjectionHidesGradesForEveryViewer(t *testing.T) {
	band, score := 8.0, 38
	original := Session{ID: uuid.New(), Status: SessionInProgress, OverallBand: &band, Sections: []SessionSection{{Attempt: attempts.Attempt{Band: &band, Score: &score, MaxScore: &score}}}}
	for _, role := range []string{"GUEST", "STUDENT"} {
		got := SessionForViewer(auth.WithUser(context.Background(), uuid.New(), role), original)
		if got.OverallBand != nil || !got.Sections[0].Attempt.ReviewLocked || got.Sections[0].Attempt.Band != nil || got.Sections[0].Attempt.Score != nil || got.Sections[0].Attempt.MaxScore != nil {
			t.Fatalf("%s leaks results: %+v", role, got)
		}
	}
	if original.OverallBand == nil || original.Sections[0].Attempt.Band == nil {
		t.Fatal("projection mutated stored report")
	}
}

func TestGuestCannotFinishAnIncompleteMockToUnlockReviews(t *testing.T) {
	service, _, _, _, testItem := setupTestFullMockService(t)
	owner := uuid.New()
	ctx := auth.WithUser(context.Background(), owner, "GUEST")
	session, _, err := service.Start(ctx, owner, testItem.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Finish(ctx, owner, session.ID); !errors.Is(err, ErrSectionIncomplete) {
		t.Fatalf("guest bypassed exam completion: %v", err)
	}
}
