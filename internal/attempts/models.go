package attempts

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound             = errors.New("attempt not found")
	ErrMaterialNotFound     = errors.New("material not found or not published")
	ErrUnsupportedMaterial  = errors.New("unsupported material type")
	ErrAlreadySubmitted     = errors.New("attempt already submitted")
	ErrWritingIncomplete    = errors.New("each writing task requires a response")
	ErrSpeakingIncomplete   = errors.New("all speaking parts need a recording or transcript")
	ErrRecordingNotFound    = errors.New("speaking recording not found")
	ErrRecordingTooLarge    = errors.New("speaking recording is too large for AI assessment")
	ErrAIUnavailable        = errors.New("AI evaluation is not configured")
	ErrAIEvaluationFailed   = errors.New("AI evaluation failed")
	ErrExamDeadlineExceeded = errors.New("exam session deadline exceeded")
	ErrSectionLocked        = errors.New("exam section is locked")
)

const (
	StatusInProgress = "IN_PROGRESS"
	StatusProcessing = "PROCESSING"
	StatusSubmitted  = "SUBMITTED"
	StatusAbandoned  = "ABANDONED"

	MaterialListening = "listening"
	MaterialReading   = "reading"
	MaterialWriting   = "writing"
	MaterialSpeaking  = "speaking"
)

type Attempt struct {
	ID                uuid.UUID  `json:"id"`
	UserID            uuid.UUID  `json:"-"`
	MaterialType      string     `json:"materialType"`
	MaterialID        uuid.UUID  `json:"materialId"`
	MaterialVersionID uuid.UUID  `json:"materialVersionId"`
	Status            string     `json:"status"`
	Score             *int       `json:"score"`
	MaxScore          *int       `json:"maxScore"`
	Band              *float64   `json:"band"`
	StartedAt         time.Time  `json:"startedAt"`
	SubmittedAt       *time.Time `json:"submittedAt"`
}

type AnswerInput struct {
	QuestionID uuid.UUID      `json:"questionId"`
	Answer     map[string]any `json:"answer"`
}

type SaveAnswersInput struct {
	Answers []AnswerInput `json:"answers"`
}

type Answer struct {
	QuestionID    uuid.UUID      `json:"questionId"`
	Answer        map[string]any `json:"answer"`
	IsCorrect     *bool          `json:"isCorrect,omitempty"`
	PointsAwarded *int           `json:"pointsAwarded,omitempty"`
}

// GradingMaterial is the module-neutral view of a material version used for
// grading and review: listening flattens parts/groups, reading flattens
// question groups. Number is the listening question number; reading
// questions have no number, so their position is used.
type GradingQuestion struct {
	ID          uuid.UUID
	Number      int
	Prompt      string
	Content     map[string]any
	Answer      map[string]any
	Explanation string
	Points      int
}

type GradingMaterial struct {
	ExamType      string
	Questions     []GradingQuestion
	WritingTasks  []WritingTask
	SpeakingParts []SpeakingPart
}

type WritingTask struct {
	ID              uuid.UUID
	Position        int
	Type            string
	Prompt          string
	MinimumWords    int
	VisualType      string
	EssayType       string
	AssessmentNotes string
}

type WritingCriterion struct {
	Band     float64 `json:"band"`
	Feedback string  `json:"feedback"`
}

type WritingCriteria struct {
	TaskResponse    WritingCriterion `json:"taskResponse"`
	Coherence       WritingCriterion `json:"coherence"`
	LexicalResource WritingCriterion `json:"lexicalResource"`
	Grammar         WritingCriterion `json:"grammar"`
}

type WritingTaskFeedback struct {
	TaskID       uuid.UUID       `json:"taskId"`
	Position     int             `json:"position"`
	Band         float64         `json:"band"`
	Criteria     WritingCriteria `json:"criteria"`
	Feedback     string          `json:"feedback"`
	Strengths    []string        `json:"strengths"`
	Improvements []string        `json:"improvements"`
}

type WritingEvaluation struct {
	AttemptID   uuid.UUID             `json:"-"`
	Model       string                `json:"model"`
	OverallBand float64               `json:"overallBand"`
	Criteria    WritingCriteria       `json:"criteria"`
	Summary     string                `json:"summary"`
	Tasks       []WritingTaskFeedback `json:"tasks"`
	EvaluatedAt time.Time             `json:"evaluatedAt"`
}

type SpeakingPart struct {
	ID                 uuid.UUID
	Position           int
	Type               string
	Title              string
	Instructions       string
	CueCard            []string
	PreparationSeconds int
	ResponseSeconds    int
	Questions          []string
}

type SpeakingCriterion struct {
	Band     float64 `json:"band"`
	Feedback string  `json:"feedback"`
}

type SpeakingCriteria struct {
	Fluency         SpeakingCriterion `json:"fluency"`
	LexicalResource SpeakingCriterion `json:"lexicalResource"`
	Grammar         SpeakingCriterion `json:"grammar"`
	Pronunciation   SpeakingCriterion `json:"pronunciation"`
}

type SpeakingPartFeedback struct {
	PartID       uuid.UUID `json:"partId"`
	Transcript   string    `json:"transcript"`
	Feedback     string    `json:"feedback"`
	Strengths    []string  `json:"strengths"`
	Improvements []string  `json:"improvements"`
}

type SpeakingEvaluation struct {
	AttemptID              uuid.UUID              `json:"-"`
	Model                  string                 `json:"model"`
	OverallBand            float64                `json:"overallBand"`
	Criteria               SpeakingCriteria       `json:"criteria"`
	Summary                string                 `json:"summary"`
	Parts                  []SpeakingPartFeedback `json:"parts"`
	EvaluatedAt            time.Time              `json:"evaluatedAt"`
	PronunciationAvailable bool                   `json:"pronunciationAvailable"`
}

type SpeakingWord struct {
	Word        string  `json:"word"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Probability float64 `json:"probability"`
}

type SpeakingSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type SpeakingMetrics struct {
	RecordingDurationSeconds float64        `json:"recordingDurationSeconds"`
	SpeechDurationSeconds    float64        `json:"speechDurationSeconds"`
	WordCount                int            `json:"wordCount"`
	SpeechRateWPM            float64        `json:"speechRateWpm"`
	ArticulationRateWPM      float64        `json:"articulationRateWpm"`
	LongPauseCount           int            `json:"longPauseCount"`
	TotalLongPauseSeconds    float64        `json:"totalLongPauseSeconds"`
	AverageLongPauseSeconds  float64        `json:"averageLongPauseSeconds"`
	MaxPauseSeconds          float64        `json:"maxPauseSeconds"`
	FillerCount              int            `json:"fillerCount"`
	Fillers                  map[string]int `json:"fillers"`
}

type SpeakingTranscription struct {
	RecordingID         uuid.UUID         `json:"-"`
	RecordingRevision   int               `json:"recordingRevision"`
	Status              string            `json:"status"`
	Provider            string            `json:"provider,omitempty"`
	Model               string            `json:"model,omitempty"`
	Language            string            `json:"language,omitempty"`
	LanguageProbability float64           `json:"languageProbability,omitempty"`
	Transcript          string            `json:"transcript,omitempty"`
	AudioDurationMS     int64             `json:"audioDurationMs,omitempty"`
	SpeechDurationMS    int64             `json:"speechDurationMs,omitempty"`
	ProcessingTimeMS    int64             `json:"processingTimeMs,omitempty"`
	Words               []SpeakingWord    `json:"words,omitempty"`
	Segments            []SpeakingSegment `json:"segments,omitempty"`
	Metrics             SpeakingMetrics   `json:"metrics"`
	Attempts            int               `json:"attempts"`
	ErrorCode           string            `json:"errorCode,omitempty"`
	ErrorMessage        string            `json:"errorMessage,omitempty"`
	StartedAt           *time.Time        `json:"startedAt,omitempty"`
	CompletedAt         *time.Time        `json:"completedAt,omitempty"`
	CreatedAt           time.Time         `json:"createdAt"`
	UpdatedAt           time.Time         `json:"updatedAt"`
}

type SpeakingAssessmentJob struct {
	Status       string `json:"status"`
	Attempts     int    `json:"attempts"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type SpeakingRecording struct {
	ID            uuid.UUID              `json:"id"`
	AttemptID     uuid.UUID              `json:"-"`
	PartID        uuid.UUID              `json:"partId"`
	OriginalName  string                 `json:"originalName"`
	MimeType      string                 `json:"mimeType"`
	StorageKey    string                 `json:"-"`
	ByteSize      int64                  `json:"byteSize"`
	Revision      int                    `json:"revision"`
	Transcription *SpeakingTranscription `json:"transcription,omitempty"`
	CreatedAt     time.Time              `json:"createdAt"`
	UpdatedAt     time.Time              `json:"updatedAt"`
}

type Summary struct {
	Attempt
	TestTitle string `json:"testTitle"`
	TestSlug  string `json:"testSlug"`
}

type ReviewAnswer struct {
	QuestionID    uuid.UUID      `json:"questionId"`
	Number        int            `json:"number"`
	Prompt        string         `json:"prompt"`
	Answer        map[string]any `json:"answer"`
	IsCorrect     bool           `json:"isCorrect"`
	PointsAwarded int            `json:"pointsAwarded"`
	CorrectAnswer map[string]any `json:"correctAnswer"`
	Explanation   string         `json:"explanation"`
}

type Detail struct {
	Attempt
	Answers            []Answer               `json:"answers,omitempty"`
	Review             []ReviewAnswer         `json:"review,omitempty"`
	WritingEvaluation  *WritingEvaluation     `json:"writingEvaluation,omitempty"`
	SpeakingEvaluation *SpeakingEvaluation    `json:"speakingEvaluation,omitempty"`
	Recordings         []SpeakingRecording    `json:"recordings,omitempty"`
	SpeakingAssessment *SpeakingAssessmentJob `json:"speakingAssessment,omitempty"`
}

// MistakeReport combines a submitted attempt with the material needed by the
// mistakes page. Objective skills contain only incorrect review items, while
// Writing and Speaking contain their AI evaluation.
type MistakeReport struct {
	Attempt            Summary             `json:"attempt"`
	Review             []ReviewAnswer      `json:"review,omitempty"`
	WritingEvaluation  *WritingEvaluation  `json:"writingEvaluation,omitempty"`
	SpeakingEvaluation *SpeakingEvaluation `json:"speakingEvaluation,omitempty"`
}
