package phoneverification

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidCode         = errors.New("verification code is invalid or expired")
	ErrResendTooSoon       = errors.New("verification code was requested too recently")
	ErrSenderNotConfigured = errors.New("whatsapp sender is not configured")
	ErrDeliveryFailed      = errors.New("whatsapp delivery failed")
)

type Purpose string

const (
	PurposeWaitlist     Purpose = "WAITLIST"
	PurposeRegistration Purpose = "REGISTRATION"
)

type Challenge struct {
	ID          uuid.UUID
	Phone       string
	Purpose     Purpose
	CodeHash    []byte
	MaxAttempts int
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type SendInput struct {
	Phone   string
	Purpose string
}

type SendResult struct {
	VerificationID uuid.UUID `json:"verificationId"`
	ExpiresAt      time.Time `json:"expiresAt"`
	RetryAfter     int64     `json:"retryAfter"`
}

type ConfirmInput struct {
	VerificationID uuid.UUID
	Phone          string
	Purpose        string
	Code           string
}

type ConfirmResult struct {
	VerificationToken string    `json:"verificationToken"`
	ExpiresAt         time.Time `json:"expiresAt"`
}
