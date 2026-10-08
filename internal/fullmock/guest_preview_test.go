package fullmock

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/google/uuid"
)

func TestGuestSessionKeepsOverallButHidesEverySectionGrade(t *testing.T) {
	band, overall, score := 7.5, 6.5, 30
	original := Session{OverallBand: &overall}
	for _, skill := range []string{"listening", "reading", "writing", "speaking"} {
		original.Sections = append(original.Sections, SessionSection{Skill: skill, Attempt: attempts.Attempt{Band: &band, Score: &score, MaxScore: &score}})
	}
	guest := auth.WithUser(context.Background(), uuid.New(), "GUEST")
	got := SessionForViewer(guest, original)
	body, _ := json.Marshal(got)
	if !got.ResultsLocked || got.OverallBand == nil || *got.OverallBand != overall || strings.Contains(string(body), "7.5") || strings.Contains(string(body), ":30") {
		t.Fatalf("session leaks section results: %s", body)
	}
	for _, section := range original.Sections {
		if section.Attempt.Band == nil || section.Attempt.Score == nil {
			t.Fatal("projection mutated internal session grades")
		}
	}
	if !reflect.DeepEqual(SessionForViewer(auth.WithUser(context.Background(), uuid.New(), "STUDENT"), original), original) {
		t.Fatal("registered session changed")
	}
}
