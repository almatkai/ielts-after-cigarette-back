package ailimits

import "time"

type Limits struct {
	AssistantLimit      int64     `json:"assistantLimit"`
	GuestAssistantLimit int64     `json:"guestAssistantLimit"`
	WritingLimit        int64     `json:"writingLimit"`
	SpeakingLimit       int64     `json:"speakingLimit"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type UpdateLimitsInput struct {
	AssistantLimit      *int64 `json:"assistantLimit"`
	GuestAssistantLimit *int64 `json:"guestAssistantLimit"`
	WritingLimit        *int64 `json:"writingLimit"`
	SpeakingLimit       *int64 `json:"speakingLimit"`
}

type Result struct {
	Allowed   bool      `json:"allowed"`
	Current   int64     `json:"current"`
	Limit     int64     `json:"limit"`
	Remaining int64     `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}
