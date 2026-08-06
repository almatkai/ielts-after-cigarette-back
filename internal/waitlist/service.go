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
	input.Phone = phoneverification.NormalizePhone(input.Phone)
	input.Source = strings.TrimSpace(input.Source)
	input.GoogleToken = strings.TrimSpace(input.GoogleToken)

	details := make(map[string]string)
	if detail := validRequiredName(input.FirstName); detail != "" {
		details["firstName"] = detail
	}
	if detail := validRequiredName(input.LastName); detail != "" {
		details["lastName"] = detail
	}
	if !phoneverification.ValidPhone(input.Phone) {
		details["phone"] = "must use E.164 format, for example +77001234567"
	}
	if utf8.RuneCountInString(input.Source) > 50 {
		details["source"] = "must contain at most 50 characters"
	}

	googleSub := ""
	email := ""
	if input.GoogleToken == "" {
		details["googleToken"] = "is required"
	} else {
		claims, err := s.verifier.Verify(ctx, input.GoogleToken)
		if err != nil {
			details["googleToken"] = "is invalid"
		} else {
			email = strings.ToLower(strings.TrimSpace(claims.Email))
			if email == "" || !claims.EmailVerified || !validEmail(email) {
				details["googleToken"] = "does not provide a verified email"
				email = ""
			} else {
				googleSub = claims.Sub
			}
		}
	}
	if len(details) > 0 {
		return Entry{}, details, nil
	}

	entry, err := s.repository.Create(ctx, CreateParams{
		FirstName: input.FirstName,
		LastName:  input.LastName,
		Email:     email,
		Phone:     input.Phone,
		Source:    input.Source,
		GoogleSub: googleSub,
		CreatedAt: s.now().UTC(),
	})
	return entry, nil, err
}

// Check lets the client short-circuit duplicates before a join is attempted:
// it reports whether the Google account already holds a seat and, when a phone
// is supplied, whether that number belongs to an existing entry. It needs a
// valid Google token so the phone lookup cannot be used to enumerate entries.
func (s *Service) Check(ctx context.Context, input CheckInput) (CheckResult, map[string]string, error) {
	input.GoogleToken = strings.TrimSpace(input.GoogleToken)
	input.Phone = phoneverification.NormalizePhone(strings.TrimSpace(input.Phone))

	details := make(map[string]string)

	googleSub := ""
	if input.GoogleToken == "" {
		details["googleToken"] = "is required"
	} else {
		claims, err := s.verifier.Verify(ctx, input.GoogleToken)
		if err != nil {
			details["googleToken"] = "is invalid"
		} else {
			googleSub = claims.Sub
		}
	}
	if input.Phone != "" && !phoneverification.ValidPhone(input.Phone) {
		details["phone"] = "must use E.164 format, for example +77001234567"
	}
	if len(details) > 0 {
		return CheckResult{}, details, nil
	}

	var result CheckResult
	registered, err := s.repository.ExistsByGoogleSub(ctx, googleSub)
	if err != nil {
		return CheckResult{}, nil, err
	}
	result.AccountRegistered = registered

	if input.Phone != "" && !result.AccountRegistered {
		taken, err := s.repository.ExistsByPhone(ctx, input.Phone)
		if err != nil {
			return CheckResult{}, nil, err
		}
		result.PhoneTaken = taken
	}
	return result, nil, nil
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
