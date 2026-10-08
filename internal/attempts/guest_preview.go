package attempts

import (
	"context"
	"fmt"
	"sort"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/listening"
)

// AttemptForViewer is an HTTP projection, not a grading operation. Full Mock
// still uses the original section bands to calculate its public overall band.
func AttemptForViewer(ctx context.Context, item Attempt) Attempt {
	if auth.Role(ctx) == "GUEST" {
		item.Band, item.Score, item.MaxScore = nil, nil, nil
	}
	return item
}

// DetailForViewer selects the same first 30% on every request. Reloading or
// changing question order must never provide a new batch of free mistakes.
func DetailForViewer(ctx context.Context, detail Detail) Detail {
	if auth.Role(ctx) != "GUEST" {
		return detail
	}
	detail.Attempt = AttemptForViewer(ctx, detail.Attempt)
	preview := &GuestPreview{}
	if detail.Status == StatusSubmitted {
		detail.Review = append([]ReviewAnswer(nil), detail.Review...)
		sort.SliceStable(detail.Review, func(i, j int) bool {
			if detail.Review[i].Number != detail.Review[j].Number {
				return detail.Review[i].Number < detail.Review[j].Number
			}
			return detail.Review[i].QuestionID.String() < detail.Review[j].QuestionID.String()
		})
		for _, item := range detail.Review {
			if !item.IsCorrect {
				preview.TotalMistakes++
			}
		}
		if evaluation := detail.WritingEvaluation; evaluation != nil {
			for _, task := range evaluation.Tasks {
				for _, text := range task.Improvements {
					preview.Improvements = append(preview.Improvements, GuestImprovement{Label: fmt.Sprintf("Task %d", task.Position), Text: text})
				}
			}
		}
		if evaluation := detail.SpeakingEvaluation; evaluation != nil {
			for index, part := range evaluation.Parts {
				for _, text := range part.Improvements {
					preview.Improvements = append(preview.Improvements, GuestImprovement{Label: fmt.Sprintf("Part %d", index+1), Text: text})
				}
			}
		}
		preview.TotalMistakes += len(preview.Improvements)
		preview.AvailableMistakes = preview.TotalMistakes * 3 / 10
		available := preview.AvailableMistakes
		for index, item := range detail.Review {
			if item.IsCorrect {
				// The student's correct answer is already known; no extra grading
				// commentary is released outside the mistake preview.
				item = publicReviewItem(item)
			} else if available > 0 {
				available--
			} else {
				item = publicReviewItem(item)
				item.Locked = true
			}
			detail.Review[index] = item
		}
		for index := range preview.Improvements {
			item := &preview.Improvements[index]
			item.Number = index + 1
			item.Locked = index >= available
			if item.Locked {
				item.Text = ""
			}
		}
	}
	// AI feedback, task/criterion bands and saved grading metadata could bypass
	// the paywall. Keep saved drafts/recordings only while the section is running.
	detail.WritingEvaluation, detail.SpeakingEvaluation = nil, nil
	if detail.Status == StatusSubmitted {
		detail.Answers, detail.Recordings = nil, nil
		detail.GuestPreview = preview
	}
	return detail
}

func publicReviewItem(item ReviewAnswer) ReviewAnswer {
	return ReviewAnswer{
		QuestionID: item.QuestionID, Number: item.Number, Prompt: item.Prompt,
		Type: item.Type, Answer: item.Answer, IsCorrect: item.IsCorrect,
	}
}

// Listening's public structure normally includes the whole transcript. Guests
// receive audio and question structure, but not a second route to locked review.
func MaterialForViewer(ctx context.Context, material any) any {
	if auth.Role(ctx) != "GUEST" {
		return material
	}
	if test, ok := material.(listening.PublicTest); ok {
		test.Parts = append([]listening.PublicPart(nil), test.Parts...)
		for index := range test.Parts {
			test.Parts[index].Transcript = ""
			test.Parts[index].TranscriptSegments = nil
		}
		return test
	}
	return material
}
