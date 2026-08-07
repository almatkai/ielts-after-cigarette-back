package waitlist

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrEntryExists           = errors.New("waitlist entry already exists")
	ErrReferralCodeCollision = errors.New("waitlist referral code already exists")
)

type JoinInput struct {
	FirstName   string
	LastName    string
	Email       string
	Phone       string
	Source      string
	GoogleToken string
	Ref         string
}

type CreateParams struct {
	FirstName      string
	LastName       string
	Email          string
	Phone          string
	Source         string
	GoogleSub      string
	ReferralCode   string
	ReferredByCode string
	CreatedAt      time.Time
}

type CheckInput struct {
	GoogleToken string
	Phone       string
}

type CheckResult struct {
	AccountRegistered bool `json:"accountRegistered"`
	PhoneTaken        bool `json:"phoneTaken"`
}

// Admin is a super admin as exposed to the admin API. Source is "env" for the
// bootstrap accounts from SUPER_ADMIN_EMAILS (cannot be removed at runtime)
// or "db" for accounts added by an existing super admin.
type Admin struct {
	Email  string `json:"email"`
	Source string `json:"source"`
}

type Entry struct {
	ID              uuid.UUID  `json:"id"`
	Phone           string     `json:"phone"`
	Email           *string    `json:"email"`
	FirstName       *string    `json:"firstName"`
	LastName        *string    `json:"lastName"`
	Source          *string    `json:"source"`
	GoogleSub       *string    `json:"googleSub,omitempty"`
	ReferralCode    string     `json:"referralCode"`
	ReferredByCode  *string    `json:"referredByCode"`
	Referrals       int        `json:"referrals"`
	Status          string     `json:"status"`
	PhoneVerifiedAt *time.Time `json:"phoneVerifiedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}
