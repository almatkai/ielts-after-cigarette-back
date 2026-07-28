package user

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("profile not found")

type Profile struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	Role        string    `json:"role"`
	CurrentBand *float64  `json:"currentBand"`
	TargetBand  *float64  `json:"targetBand"`
	ExamDate    *string   `json:"examDate"`
	ExamType    *string   `json:"examType"`
	Timezone    string    `json:"timezone"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type ProfilePatch struct {
	DisplayName *string
	Timezone    *string
}

type GoalInput struct {
	TargetBand *float64
	ExamDate   *string
	ExamType   *string
}
