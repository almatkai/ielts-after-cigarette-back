package billing

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("billing resource not found")
	ErrValidation          = errors.New("invalid billing input")
	ErrProviderUnavailable = errors.New("payment provider unavailable")
	ErrInvalidCallback     = errors.New("invalid payment callback")
)
var Skills = []string{"listening", "reading", "writing", "speaking"}

type Plan struct {
	Code          string  `json:"code"`
	Title         string  `json:"title"`
	ListPrice     int64   `json:"listPrice"`
	SalePrice     *int64  `json:"salePrice"`
	Price         int64   `json:"price"`
	AccessDays    int     `json:"accessDays"`
	MeetingsCount int     `json:"meetingsCount"`
	MeetingsKind  *string `json:"meetingsKind"`
	Active        bool    `json:"-"`
}
type Settings struct {
	FreeListening           int       `json:"freeListening"`
	FreeReading             int       `json:"freeReading"`
	FreeWriting             int       `json:"freeWriting"`
	FreeSpeaking            int       `json:"freeSpeaking"`
	FreeAssistantDailyLimit int64     `json:"freeAssistantDailyLimit"`
	UpdatedAt               time.Time `json:"updatedAt"`
	PaywallEnabled          bool      `json:"paywallEnabled"`
}

func (s Settings) Limits() map[string]int {
	return map[string]int{"listening": s.FreeListening, "reading": s.FreeReading, "writing": s.FreeWriting, "speaking": s.FreeSpeaking}
}

type Quota struct {
	Limit     int `json:"limit"`
	Used      int `json:"used"`
	Remaining int `json:"remaining"`
}
type PlanView struct {
	Code  string `json:"code"`
	Title string `json:"title"`
}
type AssistantQuota struct {
	Limit     int64 `json:"limit"`
	Remaining int64 `json:"remaining"`
}
type Access struct {
	PaywallEnabled bool             `json:"paywallEnabled"`
	Guest          bool             `json:"guest"`
	Staff          bool             `json:"staff"`
	Active         bool             `json:"active"`
	Plan           *PlanView        `json:"plan"`
	EndsAt         *time.Time       `json:"endsAt"`
	QueuedUntil    *time.Time       `json:"queuedUntil"`
	MeetingsTotal  int              `json:"meetingsTotal"`
	MeetingsUsed   int              `json:"meetingsUsed"`
	Free           map[string]Quota `json:"free"`
	Assistant      AssistantQuota   `json:"assistant"`
}
type Order struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"userId,omitempty"`
	Email           string     `json:"email,omitempty"`
	Status          string     `json:"status"`
	PlanCode        string     `json:"planCode"`
	Amount          int64      `json:"amount"`
	ListPrice       int64      `json:"listPrice"`
	CreatedAt       time.Time  `json:"createdAt"`
	PaidAt          *time.Time `json:"paidAt"`
	Currency        string     `json:"-"`
	Provider        string     `json:"provider"`
	ProviderOrderID *string    `json:"providerOrderId"`
	AccessDays      int        `json:"-"`
	MeetingsCount   int        `json:"-"`
}
type Entitlement struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"userId"`
	PlanCode      string     `json:"planCode"`
	OrderID       *uuid.UUID `json:"orderId"`
	Source        string     `json:"source"`
	StartsAt      time.Time  `json:"startsAt"`
	EndsAt        time.Time  `json:"endsAt"`
	RevokedAt     *time.Time `json:"revokedAt"`
	MeetingsTotal int        `json:"meetingsTotal"`
	MeetingsUsed  int        `json:"meetingsUsed"`
	GrantedBy     *uuid.UUID `json:"grantedBy"`
	Note          string     `json:"note"`
	CreatedAt     time.Time  `json:"createdAt"`
}
type GrantInput struct {
	PlanCode string `json:"planCode"`
	Days     *int   `json:"days"`
	Source   string `json:"source"`
	Note     string `json:"note"`
}
type PlanInput struct {
	ListPrice *int64 `json:"listPrice"`
	SalePrice *int64 `json:"salePrice"`
	Active    *bool  `json:"active"`
}
type Notification struct {
	OrderID         uuid.UUID
	ProviderOrderID string
	Status          string
	Amount          int64
	Currency        string
	Payload         map[string]any
}
type PaymentRequired struct {
	Feature   string
	Skill     string
	Remaining map[string]int
}

func (e *PaymentRequired) Error() string {
	if e.Feature == "assistant" {
		return "Бесплатные сообщения ассистента на сегодня закончились."
	}
	return "Бесплатные тесты по этому навыку закончились. Выберите тариф, чтобы продолжить."
}
