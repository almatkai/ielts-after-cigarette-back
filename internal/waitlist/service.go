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
	verifier   GoogleTokenVerifier
	now        func() time.Time
}

func NewService(repository Repository, verifier GoogleTokenVerifier) *Service {
	return &Service{repository: repository, verifier: verifier, now: time.Now}
}

func (s *Service) Join(ctx context.Context, input JoinInput) (Entry, map[string]string, error) {
	input.FirstName = strings.TrimSpace(input.FirstName)
	input.LastName = strings.TrimSpace(input.LastName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Phone = phoneverification.NormalizePhone(input.Phone)
	input.Source = strings.TrimSpace(input.Source)
	input.VerificationToken = strings.TrimSpace(input.VerificationToken)
	input.GoogleToken = strings.TrimSpace(input.GoogleToken)

	googleSub := ""
	if input.GoogleToken != "" {
		claims, err := s.verifier.Verify(ctx, input.GoogleToken)
		if err != nil {
			return Entry{}, map[string]string{"googleToken": "is invalid"}, nil
		}
		googleSub = claims.Sub
		if claims.EmailVerified && strings.TrimSpace(claims.Email) != "" {
			input.Email = strings.ToLower(strings.TrimSpace(claims.Email))
		}
	}

	details := make(map[string]string)
	if detail := validRequiredName(input.FirstName); detail != "" {
		details["firstName"] = detail
	}
	if detail := validRequiredName(input.LastName); detail != "" {
		details["lastName"] = detail
	}
	if input.Email == "" {
		details["email"] = "is required"
	} else if !validEmail(input.Email) {
		details["email"] = "must be a valid email address"
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
		FirstName:             input.FirstName,
		LastName:              input.LastName,
		Email:                 input.Email,
		Phone:                 input.Phone,
		Source:                input.Source,
		VerificationTokenHash: phoneverification.HashVerificationToken(input.VerificationToken),
		GoogleSub:             googleSub,
		CreatedAt:             s.now().UTC(),
	})
	return entry, nil, err
}

func validRequiredName(value string) string {
	switch length := utf8.RuneCountInString(value); {
	case length == 0:
		return "is required"
	case length < 2 || length > 100:
		return "must contain between 2 and 100 characters"
	}
	return ""
}

func validEmail(value string) bool {
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}
