package waitlist

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/phoneverification"
)

type Service struct {
	repository  Repository
	verifier    GoogleTokenVerifier
	adminEmails map[string]struct{}
	now         func() time.Time
}

func NewService(repository Repository, verifier GoogleTokenVerifier, adminEmails []string) *Service {
	admins := make(map[string]struct{}, len(adminEmails))
	for _, email := range adminEmails {
		if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
			admins[email] = struct{}{}
		}
	}
	return &Service{repository: repository, verifier: verifier, adminEmails: admins, now: time.Now}
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

	// A bad ref never blocks the join: it is only attribution metadata — a
	// referrer's code or a campaign tag like "instagram" — with no FK.
	referredByCode := sanitizeRef(input.Ref)

	for attempt := 0; attempt < maxReferralCodeAttempts; attempt++ {
		referralCode, err := newReferralCode()
		if err != nil {
			return Entry{}, nil, fmt.Errorf("generate referral code: %w", err)
		}
		entry, err := s.repository.Create(ctx, CreateParams{
			FirstName:      input.FirstName,
			LastName:       input.LastName,
			Email:          email,
			Phone:          input.Phone,
			Source:         input.Source,
			GoogleSub:      googleSub,
			ReferralCode:   referralCode,
			ReferredByCode: referredByCode,
			CreatedAt:      s.now().UTC(),
		})
		if !errors.Is(err, ErrReferralCodeCollision) {
			return entry, nil, err
		}
	}
	return Entry{}, nil, ErrReferralCodeCollision
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

var (
	ErrAdminProtected    = errors.New("environment super admin cannot be removed")
	ErrAdminEmailInvalid = errors.New("admin email is invalid")
)

// ListForAdmin returns every waitlist entry, newest first. Callers must be
// role-checked by middleware (ADMIN) before this runs.
func (s *Service) ListForAdmin(ctx context.Context) ([]Entry, error) {
	return s.repository.List(ctx)
}

// ListAdminsForAdmin returns the bootstrap (env) super admins first, then the
// super admins added at runtime, deduplicated.
func (s *Service) ListAdminsForAdmin(ctx context.Context) ([]Admin, error) {
	dbEmails, err := s.repository.ListAdmins(ctx)
	if err != nil {
		return nil, err
	}
	admins := make([]Admin, 0, len(s.adminEmails)+len(dbEmails))
	seen := make(map[string]struct{}, len(s.adminEmails)+len(dbEmails))
	for email := range s.adminEmails {
		seen[email] = struct{}{}
		admins = append(admins, Admin{Email: email, Source: "env"})
	}
	for _, email := range dbEmails {
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		admins = append(admins, Admin{Email: email, Source: "db"})
	}
	return admins, nil
}

func (s *Service) AddAdminForAdmin(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return ErrAdminEmailInvalid
	}
	return s.repository.AddAdmin(ctx, email)
}

func (s *Service) RemoveAdminForAdmin(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, ok := s.adminEmails[email]; ok {
		return ErrAdminProtected
	}
	return s.repository.RemoveAdmin(ctx, email)
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

var refPattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// sanitizeRef normalizes the free-form referral attribute. Anything that does
// not look like a code or campaign tag is dropped instead of failing the join.
func sanitizeRef(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !refPattern.MatchString(value) {
		return ""
	}
	return value
}

const (
	// The alphabet skips ambiguous characters (0, 1, i, l, o); its 32 chars
	// make every masked random byte unbiased.
	referralCodeAlphabet    = "23456789abcdefghjkmnpqrstuvwxyz"
	referralCodeLength      = 8
	maxReferralCodeAttempts = 5
)

func newReferralCode() (string, error) {
	raw := make([]byte, referralCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := make([]byte, referralCodeLength)
	for i, b := range raw {
		code[i] = referralCodeAlphabet[int(b)%len(referralCodeAlphabet)]
	}
	return string(code), nil
}
