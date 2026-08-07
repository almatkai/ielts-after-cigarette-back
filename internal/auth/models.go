package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	RoleStudent = "STUDENT"
	RoleEditor  = "EDITOR"
	RoleAdmin   = "ADMIN"
)

func NormalizeRole(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func ValidRole(value string) bool {
	switch NormalizeRole(value) {
	case RoleStudent, RoleEditor, RoleAdmin:
		return true
	default:
		return false
	}
}

var (
	ErrEmailExists        = errors.New("email already exists")
	ErrPhoneExists        = errors.New("phone already exists")
	ErrPhoneNotVerified   = errors.New("phone verification is invalid or expired")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidRefresh     = errors.New("invalid refresh token")
	ErrRefreshReuse       = errors.New("refresh token reuse detected")
	ErrUserNotFound       = errors.New("user not found")
	ErrInvalidGoogleToken = errors.New("google token is invalid")
	ErrAccountNotFound    = errors.New("account does not exist")
)

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Role         string
}

type UserView struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	Phone       string    `json:"phone,omitempty"`
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

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
	UserAgent string
	IPAddress string
}

type AuthResult struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"-"`
	TokenType    string   `json:"tokenType"`
	ExpiresIn    int64    `json:"expiresIn"`
	User         UserView `json:"user"`
}

type RegisterInput struct {
	Name              string
	Email             string
	Password          string
	ConfirmPassword   string
	AcceptedTerms     bool
	Phone             string
	VerificationToken string
	UserAgent         string
	IPAddress         string
}

type LoginInput struct {
	Email     string
	Password  string
	UserAgent string
	IPAddress string
}

type GoogleLoginInput struct {
	GoogleToken string
	UserAgent   string
	IPAddress   string
}

type SessionMetadata struct {
	UserAgent string
	IPAddress string
}
