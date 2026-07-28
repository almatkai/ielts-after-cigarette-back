package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrEmailExists        = errors.New("email already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidRefresh     = errors.New("invalid refresh token")
	ErrRefreshReuse       = errors.New("refresh token reuse detected")
	ErrUserNotFound       = errors.New("user not found")
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
	Name            string
	Email           string
	Password        string
	ConfirmPassword string
	AcceptedTerms   bool
	UserAgent       string
	IPAddress       string
}

type LoginInput struct {
	Email     string
	Password  string
	UserAgent string
	IPAddress string
}

type SessionMetadata struct {
	UserAgent string
	IPAddress string
}
