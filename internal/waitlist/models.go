package waitlist

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrEntryExists = errors.New("waitlist entry already exists")

type JoinInput struct {
	FirstName   string
	LastName    string
	Email       string
	Phone       string
	Source      string
	GoogleToken string
}

type CreateParams struct {
	FirstName string
	LastName  string
	Email     string
	Phone     string
	Source    string
	GoogleSub string
	CreatedAt time.Time
}

type Entry struct {
	ID              uuid.UUID  `json:"id"`
	Phone           string     `json:"phone"`
	Email           *string    `json:"email"`
	FirstName       *string    `json:"firstName"`
	LastName        *string    `json:"lastName"`
	Source          *string    `json:"source"`
	GoogleSub       *string    `json:"googleSub,omitempty"`
	Status          string     `json:"status"`
	PhoneVerifiedAt *time.Time `json:"phoneVerifiedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}
