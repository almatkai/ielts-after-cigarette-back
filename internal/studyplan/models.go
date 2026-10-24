// Package studyplan owns persisted, revisioned study plans. It does not grade
// work, infer calibrated IELTS scores, or start Full Mock sessions.
package studyplan

import (
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("план обучения или задание не найдено")
	ErrConflict   = errors.New("план изменился или действие недоступно; обновите план")
	ErrValidation = errors.New("некорректные параметры плана обучения")
)

var skills = []string{"listening", "reading", "writing", "speaking"}

type Settings struct {
	DailyMinutes      int   `json:"dailyMinutes"`
	StudyDays         []int `json:"studyDays"`
	WeeklyMockEnabled bool  `json:"weeklyMockEnabled"`
}
type Goal struct {
	TargetBand *float64 `json:"targetBand"`
	ExamDate   *string  `json:"examDate"`
	ExamType   *string  `json:"examType"`
}
type Response struct {
	Today           string           `json:"today"`
	Timezone        string           `json:"timezone"`
	Settings        Settings         `json:"settings"`
	Goal            Goal             `json:"goal"`
	Plan            *Plan            `json:"plan"`
	Recommendations []Recommendation `json:"recommendations"`
	Evidence        []Evidence       `json:"evidence"`
	OverallBand     *float64         `json:"overallBand"`
	Progress        []Progress       `json:"progress"`
	Limitations     []string         `json:"limitations"`
}
type Plan struct {
	ExamDate         *string  `json:"examDate,omitempty"`
	ID               string   `json:"id"`
	Revision         int64    `json:"revision"`
	StartDate        string   `json:"startDate"`
	EndDate          string   `json:"endDate"`
	Days             []Day    `json:"days"`
	Weeks            []Week   `json:"weeks"`
	Changes          []Change `json:"changes"`
	ReturnAfterPause bool     `json:"returnAfterPause"`
}
type Day struct {
	ReadOnlyReason *string      `json:"readOnlyReason,omitempty"`
	Date           string       `json:"date"`
	DayIndex       int          `json:"dayIndex"`
	Week           int          `json:"week"`
	BudgetMinutes  int          `json:"budgetMinutes"`
	PlannedMinutes int          `json:"plannedMinutes"`
	IsRest         bool         `json:"isRest"`
	Preliminary    bool         `json:"preliminary"`
	Assignments    []Assignment `json:"assignments"`
}
type Assignment struct {
	CycleID         *string `json:"cycleId,omitempty"`
	DrillID         *string `json:"drillId,omitempty"`
	SessionID       *string `json:"sessionId,omitempty"`
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	Skill           string  `json:"skill"`
	Title           string  `json:"title"`
	Reason          string  `json:"reason"`
	DurationMinutes int     `json:"durationMinutes"`
	Status          string  `json:"status"`
	MaterialID      *string `json:"materialId"`
	AttemptID       *string `json:"attemptId"`
	SourceAttemptID *string `json:"sourceAttemptId"`
	Target          string  `json:"target"`
	SkipReason      *string `json:"skipReason"`
	CompletedAt     *string `json:"completedAt"`
}
type Week struct {
	Week        int       `json:"week"`
	StartDate   string    `json:"startDate"`
	EndDate     string    `json:"endDate"`
	Goal        string    `json:"goal"`
	Preliminary bool      `json:"preliminary"`
	MockOffer   MockOffer `json:"mockOffer"`
}
type MockOffer struct {
	Status          string  `json:"status"`
	DurationMinutes int     `json:"durationMinutes"`
	ScheduledDate   *string `json:"scheduledDate"`
	WindowMinutes   *int    `json:"windowMinutes"`
	Target          string  `json:"target"`
}
type Change struct {
	Type        string  `json:"type"`
	Description string  `json:"description"`
	Date        *string `json:"date"`
}
type Recommendation struct {
	ID        string  `json:"id"`
	Skill     string  `json:"skill"`
	Title     string  `json:"title"`
	Reason    string  `json:"reason"`
	AttemptID *string `json:"attemptId"`
	Target    string  `json:"target"`
}
type Evidence struct {
	Skill        string   `json:"skill"`
	Band         *float64 `json:"band"`
	PracticeBand *float64 `json:"practiceBand"`
	Status       string   `json:"status"`
	AttemptID    *string  `json:"attemptId"`
	AssessedAt   *string  `json:"assessedAt"`
	SampleCount  int      `json:"sampleCount"`
	Score        *int     `json:"score"`
	MaxScore     *int     `json:"maxScore"`
	Source       string   `json:"source"`
	Limitations  []string `json:"limitations"`
}
type Progress struct {
	DrillsCompleted         int    `json:"drillsCompleted"`
	CorrectionsSaved        int    `json:"correctionsSaved"`
	IndependentChecksPassed int    `json:"independentChecksPassed"`
	Date                    string `json:"date"`
	CompletedAttempts       int    `json:"completedAttempts"`
	ReviewedAssignments     int    `json:"reviewedAssignments"`
	SkippedAssignments      int    `json:"skippedAssignments"`
	PlannedMinutes          int    `json:"plannedMinutes"`
	ActiveMinutes           *int   `json:"activeMinutes"`
}
type StartResponse struct {
	AttemptID    *string `json:"attemptId"`
	Target       string  `json:"target"`
	PlanRevision int64   `json:"planRevision"`
}

// State is private persisted bookkeeping, not a public test-clock or API field.
type state struct {
	Learning     learningState `json:"learning"`
	History      []Plan        `json:"history,omitempty"`
	Settings     Settings      `json:"settings"`
	Plan         Plan          `json:"plan"`
	LastDay      string        `json:"lastDay"`
	LastActivity string        `json:"lastActivity"`
	Inputs       string        `json:"inputs"`
}
type material struct {
	DrillID  string
	Unit     *drillUnit
	ID       string
	Skill    string
	Title    string
	Duration int
	Passage  bool
}
type questionEvidence struct {
	ID      string `json:"id"`
	Correct bool   `json:"correct"`
}
type result struct {
	VersionID   string
	Questions   []questionEvidence
	FromMock    bool
	Title       string
	ID          string
	Skill       string
	MaterialID  string
	Status      string
	StartedAt   time.Time
	SubmittedAt *time.Time
	Score       *int
	MaxScore    *int
	Band        *float64
	Wrong       int
	Feedback    []byte
	Repeat      bool
	Reviewable  bool
	Claimed     bool
}

func ptr[T any](value T) *T { return &value }
func defaultSettings() Settings {
	return Settings{DailyMinutes: 45, StudyDays: []int{1, 2, 3, 4, 5, 6}}
}
func validateSettings(s Settings) error {
	if s.DailyMinutes != 30 && s.DailyMinutes != 45 && s.DailyMinutes != 60 && s.DailyMinutes != 90 {
		return ErrValidation
	}
	if len(s.StudyDays) == 0 || len(s.StudyDays) > 7 {
		return ErrValidation
	}
	seen := map[int]bool{}
	for _, d := range s.StudyDays {
		if d < 1 || d > 7 || seen[d] {
			return ErrValidation
		}
		seen[d] = true
	}
	return nil
}
