package blog

import (
	"testing"
)

func TestValidateApplyInputRequiresMinimumBand(t *testing.T) {
	tests := []struct {
		name      string
		overall   float64
		wantField bool
	}{
		{name: "band below 7.5 is rejected", overall: 7.0, wantField: true},
		{name: "band 7.5 is accepted", overall: 7.5, wantField: false},
		{name: "band 9 is accepted", overall: 9.0, wantField: false},
		{name: "band above 9 is rejected", overall: 9.5, wantField: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := ApplyInput{
				OverallBand:  tt.overall,
				ReadingBand:  7.5,
				WritingBand:  7.5,
				SpeakingBand: 7.5,
				TRFNumber:    "25RU000123ALMATY01",
			}
			details := validateApplyInput(input)
			if tt.wantField {
				if _, ok := details["overallBand"]; !ok {
					t.Fatalf("expected overallBand validation error, got %v", details)
				}
				return
			}
			if len(details) != 0 {
				t.Fatalf("expected no validation errors, got %v", details)
			}
		})
	}
}

func TestValidateApplyInputRejectsNonHalfBands(t *testing.T) {
	input := ApplyInput{
		OverallBand:  7.5,
		ReadingBand:  7.25,
		WritingBand:  7.5,
		SpeakingBand: 8.0,
		TRFNumber:    "25RU000123ALMATY01",
	}
	details := validateApplyInput(input)
	if _, ok := details["readingBand"]; !ok {
		t.Fatalf("expected readingBand validation error, got %v", details)
	}
}

func TestValidateApplyInputRequiresTRF(t *testing.T) {
	input := ApplyInput{
		OverallBand:  7.5,
		ReadingBand:  7.5,
		WritingBand:  7.5,
		SpeakingBand: 7.5,
		TRFNumber:    "  ",
	}
	details := validateApplyInput(input)
	if _, ok := details["trfNumber"]; !ok {
		t.Fatalf("expected trfNumber validation error, got %v", details)
	}
}

func TestValidateBlogPostInput(t *testing.T) {
	service := &Service{}
	base := SaveInput{
		Slug:        "how-to-score-band-8",
		Title:       "How to score band 8",
		Description: "A short guide",
		BodyHTML:    "<p>hello</p>",
	}
	if details := service.validateInput(base); len(details) != 0 {
		t.Fatalf("expected valid input, got %v", details)
	}

	invalid := base
	invalid.Slug = "Invalid Slug"
	if details := service.validateInput(invalid); len(details["slug"]) == 0 {
		t.Fatalf("expected slug validation error, got %v", details)
	}

	invalid = base
	invalid.Title = "ab"
	if details := service.validateInput(invalid); len(details["title"]) == 0 {
		t.Fatalf("expected title validation error, got %v", details)
	}

	invalid = base
	invalid.BodyHTML = ""
	if details := service.validateInput(invalid); len(details["bodyHtml"]) == 0 {
		t.Fatalf("expected bodyHtml validation error, got %v", details)
	}
}

func TestEstimateReadingTime(t *testing.T) {
	if got := estimateReadingTime("<p>one two three</p>"); got != 1 {
		t.Fatalf("expected 1 minute for a short post, got %d", got)
	}
	words := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		words = append(words, "word")
	}
	if got := estimateReadingTime("<p>" + joinWords(words) + "</p>"); got != 3 {
		t.Fatalf("expected 3 minutes for 600 words, got %d", got)
	}
}

func joinWords(words []string) string {
	out := ""
	for i, word := range words {
		if i > 0 {
			out += " "
		}
		out += word
	}
	return out
}
