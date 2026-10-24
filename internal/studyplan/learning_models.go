package studyplan

import "encoding/json"

type MicroSkill struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Title   string `json:"title"`
}
type Provenance struct {
	Model         *string `json:"model"`
	PromptVersion *string `json:"promptVersion"`
	RubricVersion *string `json:"rubricVersion"`
}
type SourceEvidence struct {
	AudioURL       *string    `json:"audioUrl,omitempty"`
	SessionID      *string    `json:"sessionId,omitempty"`
	DrillID        *string    `json:"drillId,omitempty"`
	AttemptID      string     `json:"attemptId"`
	QuestionID     *string    `json:"questionId"`
	MaterialID     string     `json:"materialId"`
	VersionID      string     `json:"versionId"`
	FamilyID       *string    `json:"familyId"`
	FamilyKnown    bool       `json:"familyKnown"`
	QuestionType   string     `json:"questionType"`
	Prompt         string     `json:"prompt"`
	Stimulus       string     `json:"stimulus"`
	OriginalAnswer string     `json:"originalAnswer"`
	Feedback       string     `json:"feedback"`
	OccurredAt     string     `json:"occurredAt"`
	Modality       string     `json:"modality"`
	Provenance     Provenance `json:"provenance"`
}
type Correction struct {
	Text        string `json:"text"`
	Explanation string `json:"explanation"`
	Assisted    bool   `json:"assisted"`
	SavedAt     string `json:"savedAt"`
	Verdict     string `json:"verdict"`
	Feedback    string `json:"feedback"`
}
type TransferCheck struct {
	SessionID   string  `json:"sessionId"`
	FamilyID    *string `json:"familyId"`
	OccurredAt  string  `json:"occurredAt"`
	Independent bool    `json:"independent"`
	Delayed     bool    `json:"delayed"`
	Passed      bool    `json:"passed"`
	Reason      string  `json:"reason"`
}
type Cycle struct {
	ID              string          `json:"id"`
	Revision        int64           `json:"revision"`
	SkillID         string          `json:"skillId"`
	Section         string          `json:"section"`
	Status          string          `json:"status"`
	Source          SourceEvidence  `json:"source"`
	ViewedAt        *string         `json:"viewedAt"`
	Correction      *Correction     `json:"correction"`
	Checks          []TransferCheck `json:"checks"`
	SkippedAt       *string         `json:"skippedAt"`
	NextDrillID     *string         `json:"nextDrillId"`
	NextCheckDate   *string         `json:"nextCheckDate"`
	RemainingChecks int             `json:"remainingChecks"`
	Limitations     []string        `json:"limitations"`
}
type Drill struct {
	AudioURL         *string `json:"audioUrl,omitempty"`
	SourceMaterialID *string `json:"sourceMaterialId,omitempty"`
	SourceVersionID  *string `json:"sourceVersionId,omitempty"`
	SourceQuestionID *string `json:"sourceQuestionId,omitempty"`
	ID               string  `json:"id"`
	SkillID          string  `json:"skillId"`
	Section          string  `json:"section"`
	Title            string  `json:"title"`
	DurationMinutes  int     `json:"durationMinutes"`
	Modality         string  `json:"modality"`
	FamilyID         *string `json:"familyId"`
	Version          string  `json:"version"`
	Introduction     string  `json:"introduction"`
	Stimulus         string  `json:"stimulus"`
	Task             string  `json:"task"`
	RightsProvenance string  `json:"rightsProvenance"`
	EditorialStatus  string  `json:"editorialStatus"`
	MasteryEligible  bool    `json:"masteryEligible"`
}
type Session struct {
	ID                 string  `json:"id"`
	Revision           int64   `json:"revision"`
	Drill              Drill   `json:"drill"`
	CycleID            *string `json:"cycleId"`
	AssignmentID       *string `json:"assignmentId"`
	Status             string  `json:"status"`
	StartedAt          string  `json:"startedAt"`
	SubmittedAt        *string `json:"submittedAt"`
	Text               *string `json:"text"`
	Explanation        *string `json:"explanation"`
	Assisted           bool    `json:"assisted"`
	AudioAttemptID     *string `json:"audioAttemptId"`
	Verdict            *string `json:"verdict"`
	Feedback           *string `json:"feedback"`
	Independent        bool    `json:"independent"`
	IndependenceReason string  `json:"independenceReason"`
}
type WeakProfile struct {
	SkillID             string   `json:"skillId"`
	Section             string   `json:"section"`
	Status              string   `json:"status"`
	SourceIDs           []string `json:"sourceIds"`
	Rationale           string   `json:"rationale"`
	UpdatedAt           *string  `json:"updatedAt"`
	IndependentFamilies int      `json:"independentFamilies"`
	DelayedCheckPassed  bool     `json:"delayedCheckPassed"`
	RemainingChecks     int      `json:"remainingChecks"`
}
type SectionInventory struct {
	Section             string   `json:"section"`
	FullMaterials       int      `json:"fullMaterials"`
	FittingMaterials    int      `json:"fittingMaterials"`
	ShortDrills         int      `json:"shortDrills"`
	ReviewedDrills      int      `json:"reviewedDrills"`
	IndependentFamilies int      `json:"independentFamilies"`
	Available           bool     `json:"available"`
	Limitations         []string `json:"limitations"`
}
type Inventory struct {
	DailyMinutes int                `json:"dailyMinutes"`
	Sections     []SectionInventory `json:"sections"`
	Limitations  []string           `json:"limitations"`
}

// TaskPerformance describes observed question formats, never a cognitive diagnosis
// or a calibrated IELTS score. Corrections and transfer checks are separate.
type TaskPerformance struct {
	Section          string                  `json:"section"`
	QuestionType     string                  `json:"questionType"`
	Title            string                  `json:"title"`
	Answered         int                     `json:"answered"`
	Correct          int                     `json:"correct"`
	Incorrect        int                     `json:"incorrect"`
	SourceCount      int                     `json:"sourceCount"`
	PracticeAnswered int                     `json:"practiceAnswered"`
	SourceRefs       []TaskPerformanceSource `json:"sourceRefs"`
	Limitations      []string                `json:"limitations"`
}
type TaskPerformanceSource struct {
	SourceID   string `json:"sourceId"`
	AttemptID  string `json:"attemptId"`
	Title      string `json:"title"`
	OccurredAt string `json:"occurredAt"`
	Answered   int    `json:"answered"`
	Correct    int    `json:"correct"`
	Incorrect  int    `json:"incorrect"`
}
type Learning struct {
	TaskPerformance []TaskPerformance `json:"taskPerformance"`
	Revision        int64             `json:"revision"`
	Today           string            `json:"today"`
	ExamDate        *string           `json:"examDate"`
	ReadOnly        bool              `json:"readOnly"`
	Skills          []MicroSkill      `json:"skills"`
	Profiles        []WeakProfile     `json:"profiles"`
	Cycles          []Cycle           `json:"cycles"`
	Drills          []Drill           `json:"drills"`
	Inventory       Inventory         `json:"inventory"`
	Limitations     []string          `json:"limitations"`
	OverallBand     *float64          `json:"overallBand"`
}

// Private answer keys are persisted in the aggregate, never serialized into API DTOs.
type learningCycle struct {
	Cycle     Cycle           `json:"cycle"`
	Key       json.RawMessage `json:"key,omitempty"`
	Revisions []Correction    `json:"revisions,omitempty"`
	SourceKey string          `json:"sourceKey"`
}
type learningSession struct {
	CorrectionGeneration int             `json:"correctionGeneration"`
	Session              Session         `json:"session"`
	Key                  json.RawMessage `json:"key,omitempty"`
	TeacherFeedback      string          `json:"teacherFeedback"`
	SeenBefore           bool            `json:"seenBefore"`
	CycleRevision        int64           `json:"cycleRevision"`
}
type learningReceipt struct {
	Hash  string          `json:"hash"`
	Value json.RawMessage `json:"value"`
}
type learningState struct {
	AssignmentBindings map[string]learningAssignmentBinding `json:"assignmentBindings,omitempty"`
	Zone               string                               `json:"zone"`
	Cycles             []learningCycle                      `json:"cycles"`
	Sessions           []learningSession                    `json:"sessions"`
	Receipts           map[string]learningReceipt           `json:"receipts"`
}
type learningInput struct {
	ExpectedRevision int64   `json:"expectedRevision"`
	IdempotencyKey   string  `json:"idempotencyKey"`
	Text             string  `json:"text"`
	Explanation      string  `json:"explanation"`
	Assisted         *bool   `json:"assisted"`
	CycleID          *string `json:"cycleId"`
	AssignmentID     *string `json:"assignmentId"`
	AudioAttemptID   *string `json:"audioAttemptId"`
}
type learningCommand struct {
	ReceiptReplay bool
	Action        string
	ID            string
	Input         learningInput
	Out           any
}
