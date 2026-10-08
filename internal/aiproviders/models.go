package aiproviders

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMigrationRequired = errors.New("AI provider database migration is required")
	ErrUnavailable       = errors.New("AI providers are not configured")
	ErrAllFailed         = errors.New("all AI providers failed")
	ErrNotFound          = errors.New("AI provider not found")
	ErrConflict          = errors.New("AI provider was changed by another admin")
	ErrValidation        = errors.New("invalid AI provider configuration")
)

// Reserved row stores only environment-provider metadata, never its credential.
var EnvProviderID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type Provider struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Endpoint       string    `json:"endpoint"`
	Model          string    `json:"model"`
	SpeakingModel  string    `json:"speakingModel"`
	Scopes         []string  `json:"scopes"`
	Enabled        bool      `json:"enabled"`
	Priority       int       `json:"priority"`
	TimeoutSeconds int       `json:"timeoutSeconds"`
	Revision       int64     `json:"revision"`
	UpdatedAt      time.Time `json:"updatedAt"`
	HasKey         bool      `json:"hasKey"`
	Ciphertext     []byte    `json:"-"`
	APIKey         string    `json:"-"`
	FromEnv        bool      `json:"fromEnv"`
}

func (p Provider) aad() string { return "ai-provider:v1:" + p.ID.String() + ":" + p.Endpoint }

type Input struct {
	Name           string   `json:"name"`
	Endpoint       string   `json:"endpoint"`
	Model          string   `json:"model"`
	SpeakingModel  string   `json:"speakingModel"`
	Scopes         []string `json:"scopes"`
	Enabled        bool     `json:"enabled"`
	Priority       int      `json:"priority"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	APIKey         string   `json:"apiKey"`
	Revision       int64    `json:"revision"`
}

// Never attach the underlying provider response, URL, prompt or token to this
// error: it is the only error allowed into logs, GlitchTip and test responses.
type Failure struct {
	ProviderID    uuid.UUID
	Purpose, Code string
	Status        int
}

func (e *Failure) Error() string {
	return fmt.Sprintf("AI provider %s failed (%s, %s, HTTP %d)", e.ProviderID, e.Purpose, e.Code, e.Status)
}

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("AI HTTP status %d", e.Status) }
