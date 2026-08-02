package waitlist

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrEntryExists      = errors.New("waitlist entry already exists")
	ErrPhoneNotVerified = errors.New("phone verification is invalid or expired")
)

type JoinInput struct {
	Name              string
	Email             string
	Phone             string
	Source            string
	VerificationToken string
}

type CreateParams struct {
	Name                  string
	Email                 string
	Phone                 string
	Source                string
	VerificationTokenHash []byte
	CreatedAt             time.Time
}

type Entry struct {
	ID              uuid.UUID `json:"id"`
	Phone           string    `json:"phone"`
	Email           *string   `json:"email"`
	DisplayName     *string   `json:"displayName"`
	Source          *string   `json:"source"`
	Status          string    `json:"status"`
	PhoneVerifiedAt time.Time `json:"phoneVerifiedAt"`
	CreatedAt       time.Time `json:"createdAt"`
}
