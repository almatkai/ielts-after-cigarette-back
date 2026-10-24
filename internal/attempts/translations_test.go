package attempts

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeReviewLang(t *testing.T) {
	cases := map[string]string{
		"kk": "kk", "en": "en",
		"": "", "ru": "", "KK": "", "kz": "", "fr": "",
	}
	for in, want := range cases {
		if got := normalizeReviewLang(in); got != want {
			t.Errorf("normalizeReviewLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyQuestionTranslations(t *testing.T) {
	idTranslated := uuid.New()
	idUntranslated := uuid.New()
	review := []ReviewAnswer{
		{QuestionID: idTranslated, Explanation: "ru-exp", Hint: "ru-hint"},
		{QuestionID: idUntranslated, Explanation: "ru-exp2", Hint: "ru-hint2"},
	}
	tr := map[uuid.UUID]QuestionTranslation{
		idTranslated: {QuestionID: idTranslated, Lang: "kk", Explanation: "kk-exp", Hint: ""},
	}
	got := applyQuestionTranslations(review, tr)
	if got[0].Explanation != "kk-exp" {
		t.Errorf("explanation not overridden: %q", got[0].Explanation)
	}
	if got[0].Hint != "ru-hint" {
		t.Errorf("empty hint must keep source: %q", got[0].Hint)
	}
	if got[1].Explanation != "ru-exp2" || got[1].Hint != "ru-hint2" {
		t.Errorf("missing row must keep source: %+v", got[1])
	}
}

func TestLocalizeReviewSkipsWithoutLang(t *testing.T) {
	svc := &Service{repository: &stubRepository{}}
	review := []ReviewAnswer{{QuestionID: uuid.New(), Explanation: "ru"}}
	got, err := svc.localizeReview(context.Background(), review, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Explanation != "ru" {
		t.Errorf("ru review must pass through: %+v", got)
	}
}
