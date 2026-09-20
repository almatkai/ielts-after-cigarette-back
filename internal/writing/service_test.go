package writing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPublicMaterialHidesAssessmentNotes(t *testing.T) {
	t.Parallel()

	assetID := uuid.New()
	material := Material{
		ID: uuid.New(), ExamType: "academic", DurationMinutes: 60,
		Tasks: []Task{{
			ID: uuid.New(), Position: 1, Type: TaskOne, Prompt: "Describe the map.",
			MinimumWords: 150, VisualAssetID: &assetID,
			AssessmentNotes: "The private answer key must never be exposed to a student.",
		}},
	}

	payload, err := json.Marshal(publicMaterial(material))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "private answer key") || strings.Contains(string(payload), "assessmentNotes") {
		t.Fatalf("public payload leaks private assessment notes: %s", payload)
	}
	if !strings.Contains(string(payload), assetID.String()) {
		t.Fatalf("public payload does not contain visual asset reference: %s", payload)
	}
}

func TestValidateForPublishRequiresAcademicVisualContext(t *testing.T) {
	t.Parallel()

	material := Material{ExamType: "academic", Tasks: []Task{{Type: TaskOne}, {Type: TaskTwo}}}
	details := validateForPublish(material)
	if details["tasks[0].visualAssetId"] == "" {
		t.Fatal("expected a missing visual validation error")
	}
	if details["tasks[0].assessmentNotes"] == "" {
		t.Fatal("expected a missing assessment notes validation error")
	}

	assetID := uuid.New()
	material.Tasks[0].VisualAssetID = &assetID
	material.Tasks[0].AssessmentNotes = "The road gained two roundabouts and separate car parks."
	if details := validateForPublish(material); len(details) != 0 {
		t.Fatalf("expected publish validation to pass, got %#v", details)
	}
}
