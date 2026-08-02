package waitlist

import (
	"context"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/phoneverification"
)

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (s *Service) Join(ctx context.Context, input JoinInput) (Entry, map[string]string, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Phone = phoneverification.NormalizePhone(input.Phone)
	input.Source = strings.TrimSpace(input.Source)
	input.VerificationToken = strings.TrimSpace(input.VerificationToken)

	details := make(map[string]string)
	nameLength := utf8.RuneCountInString(input.Name)
	if input.Name != "" && (nameLength < 2 || nameLength > 100) {
		details["name"] = "must contain between 2 and 100 characters when provided"
	}
	if input.Email != "" && !validEmail(input.Email) {
		details["email"] = "must be a valid email address when provided"
	}
	if !phoneverification.ValidPhone(input.Phone) {
		details["phone"] = "must use E.164 format, for example +77001234567"
	}
	if utf8.RuneCountInString(input.Source) > 50 {
		details["source"] = "must contain at most 50 characters"
	}
	if !phoneverification.ValidVerificationToken(input.VerificationToken) {
		details["verificationToken"] = "is required"
	}
	if len(details) > 0 {
		return Entry{}, details, nil
	}

	entry, err := s.repository.Create(ctx, CreateParams{
		Name:                  input.Name,
		Email:                 input.Email,
		Phone:                 input.Phone,
		Source:                input.Source,
		VerificationTokenHash: phoneverification.HashVerificationToken(input.VerificationToken),
		CreatedAt:             s.now().UTC(),
	})
	return entry, nil, err
}

func validEmail(value string) bool {
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}
