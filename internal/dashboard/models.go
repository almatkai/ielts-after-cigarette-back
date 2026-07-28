package dashboard

type Response struct {
	Profile             ProfileSummary    `json:"profile"`
	RecommendedAction   RecommendedAction `json:"recommendedAction"`
	TodayPlan           []TodayPlanItem   `json:"todayPlan"`
	SkillProgress       []SkillProgress   `json:"skillProgress"`
	UnreadNotifications int               `json:"unreadNotifications"`
}

type ProfileSummary struct {
	CurrentBand *float64 `json:"currentBand"`
	TargetBand  *float64 `json:"targetBand"`
	ExamDate    *string  `json:"examDate"`
}

type RecommendedAction struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Target      string `json:"target"`
}

type TodayPlanItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Skill    string `json:"skill"`
	Duration int    `json:"durationMinutes"`
}

type SkillProgress struct {
	Skill           string   `json:"skill"`
	EstimatedBand   *float64 `json:"estimatedBand"`
	AccuracyPercent *float64 `json:"accuracyPercent"`
	CompletedTasks  int      `json:"completedTasks"`
}
