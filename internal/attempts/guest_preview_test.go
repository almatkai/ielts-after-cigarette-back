package attempts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/listening"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestGuestReviewThirtyPercentStableAndRedacted(t *testing.T) {
	ctx := auth.WithUser(context.Background(), uuid.New(), "GUEST")
	for _, count := range []int{0, 1, 2, 3, 4, 9, 10, 11, 40} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			band, score := 7.5, 22
			original := Detail{Attempt: Attempt{Status: StatusSubmitted, Band: &band, Score: &score, MaxScore: &score}}
			for number := count; number > 0; number-- {
				original.Review = append(original.Review, ReviewAnswer{
					QuestionID: uuid.New(), Number: number, Prompt: "Question", Answer: map[string]any{"value": "mine"},
					CorrectAnswer: map[string]any{"value": fmt.Sprintf("secret-%d", number)},
					Explanation:   "private explanation", Quote: "private quote", Hint: "private hint",
					Content:    map[string]any{"nested": map[string]any{"quote": "hidden"}},
					Transcript: "private transcript", PassageBody: "private passage", PointsAwarded: 2,
				})
			}
			original.Review = append(original.Review, ReviewAnswer{QuestionID: uuid.New(), Number: count + 1, IsCorrect: true, Explanation: "correct commentary"})
			before, _ := json.Marshal(original)
			got := DetailForViewer(ctx, original)
			if got.Band != nil || got.Score != nil || got.MaxScore != nil || got.GuestPreview.TotalMistakes != count || got.GuestPreview.AvailableMistakes != count*3/10 {
				t.Fatalf("bad preview: %+v", got)
			}
			for _, item := range got.Review {
				if !item.IsCorrect && item.Number <= count*3/10 {
					if item.Locked || item.CorrectAnswer == nil || item.Explanation == "" {
						t.Fatal("allowed mistake was stripped")
					}
					continue
				}
				if item.Locked != !item.IsCorrect || item.CorrectAnswer != nil || item.Explanation != "" || item.Quote != "" || item.Hint != "" || item.Content != nil || item.Transcript != "" || item.PassageBody != "" || item.PointsAwarded != 0 {
					t.Fatalf("grading data leaked: %+v", item)
				}
			}
			after, _ := json.Marshal(original)
			if string(before) != string(after) {
				t.Fatal("projection mutated cached grading detail")
			}
			if !reflect.DeepEqual(got, DetailForViewer(ctx, original)) {
				t.Fatal("reload released a different preview")
			}
			if account := DetailForViewer(auth.WithUser(context.Background(), uuid.New(), "STUDENT"), original); !reflect.DeepEqual(account, original) {
				t.Fatal("registered review changed")
			}
		})
	}
}

func TestGuestAIPreviewDoesNotExposeBandsOrOtherFeedback(t *testing.T) {
	ctx := auth.WithUser(context.Background(), uuid.New(), "GUEST")
	for _, skill := range []string{MaterialWriting, MaterialSpeaking} {
		t.Run(skill, func(t *testing.T) {
			original := Detail{Attempt: Attempt{Status: StatusSubmitted, MaterialType: skill}, Answers: []Answer{{QuestionID: uuid.New(), Answer: map[string]any{"value": "draft"}}}, Recordings: []SpeakingRecording{{ID: uuid.New()}}}
			texts := []string{"public-one", "public-two", "public-three", "hidden-four", "hidden-five", "hidden-six", "hidden-seven", "hidden-eight", "hidden-nine", "hidden-ten"}
			if skill == MaterialWriting {
				original.WritingEvaluation = &WritingEvaluation{OverallBand: 8, Summary: "private summary", Tasks: []WritingTaskFeedback{{Position: 1, Band: 8, Feedback: "private feedback", Improvements: texts}}}
			} else {
				original.SpeakingEvaluation = &SpeakingEvaluation{OverallBand: 8, Summary: "private summary", Parts: []SpeakingPartFeedback{{Feedback: "private feedback", Transcript: "private transcript", Improvements: texts}}}
			}
			got := DetailForViewer(ctx, original)
			body, _ := json.Marshal(got)
			if got.GuestPreview.TotalMistakes != 10 || got.GuestPreview.AvailableMistakes != 3 || got.WritingEvaluation != nil || got.SpeakingEvaluation != nil || got.Answers != nil || got.Recordings != nil {
				t.Fatalf("AI preview=%s", body)
			}
			for _, hidden := range append(texts[3:], "private summary", "private feedback", "private transcript", "overallBand") {
				if strings.Contains(string(body), hidden) {
					t.Fatalf("leaked %q: %s", hidden, body)
				}
			}
		})
	}
}

func TestGuestMaterialHidesFullListeningTranscriptWithoutMutation(t *testing.T) {
	original := listening.PublicTest{Parts: []listening.PublicPart{{Transcript: "private", TranscriptSegments: []listening.STTSegment{{Text: "private"}}}}}
	got := MaterialForViewer(auth.WithUser(context.Background(), uuid.New(), "GUEST"), original).(listening.PublicTest)
	if got.Parts[0].Transcript != "" || len(got.Parts[0].TranscriptSegments) != 0 || original.Parts[0].Transcript != "private" {
		t.Fatal("material leaks transcript or mutates source")
	}
	if !reflect.DeepEqual(MaterialForViewer(context.Background(), original), original) {
		t.Fatal("account material changed")
	}
}

func TestGuestAttemptHTTPDetailAndSubmitHideBand(t *testing.T) {
	service, repo := testService()
	attempt := startForTest(t, service, testUserID)
	handler := NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil)), 4096)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), testUserID, "GUEST")))
		})
	})
	router.Post("/attempts/{attemptID}/submit", handler.Submit)
	router.Get("/attempts/{attemptID}", handler.Get)
	request := httptest.NewRequest("POST", "/attempts/"+attempt.ID.String()+"/submit", strings.NewReader(`{"answers":[]}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"band":null`) || !strings.Contains(w.Body.String(), `"score":null`) {
		t.Fatalf("submit leaks grading: %d %s", w.Code, w.Body.String())
	}
	if repo.attempts[attempt.ID].Band == nil {
		t.Fatal("projection removed stored grade")
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/attempts/"+attempt.ID.String(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"availableMistakes":0`) || strings.Contains(w.Body.String(), `"accepted"`) {
		t.Fatalf("detail leaks hidden answers: %d %s", w.Code, w.Body.String())
	}
}
