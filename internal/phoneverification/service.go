package phoneverification

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	CreateChallenge(context.Context, Challenge, time.Duration) error
	VerifyChallenge(
		context.Context,
		uuid.UUID,
		string,
		Purpose,
		[]byte,
		[]byte,
		time.Time,
		time.Time,
	) error
}

type Sender interface {
	Configured() bool
	SendCode(context.Context, string, string) error
}

type Service struct {
	repository     Repository
	sender         Sender
	secret         []byte
	codeTTL        time.Duration
	tokenTTL       time.Duration
	resendInterval time.Duration
	maxAttempts    int
	now            func() time.Time
}

func NewService(
	repository Repository,
	sender Sender,
	secret string,
	codeTTL time.Duration,
	tokenTTL time.Duration,
	resendInterval time.Duration,
	maxAttempts int,
) *Service {
	return &Service{
		repository:     repository,
		sender:         sender,
		secret:         []byte(secret),
		codeTTL:        codeTTL,
		tokenTTL:       tokenTTL,
		resendInterval: resendInterval,
		maxAttempts:    maxAttempts,
		now:            time.Now,
	}
}

func (s *Service) Send(ctx context.Context, input SendInput) (SendResult, map[string]string, error) {
	phone := NormalizePhone(input.Phone)
	purpose, ok := ParsePurpose(input.Purpose)
	details := make(map[string]string)
	if !ValidPhone(phone) {
		details["phone"] = "must use E.164 format, for example +77001234567"
	}
	if !ok {
		details["purpose"] = "must be waitlist or registration"
	}
	if len(details) > 0 {
		return SendResult{}, details, nil
	}
	if !s.sender.Configured() {
		return SendResult{}, nil, ErrSenderNotConfigured
	}

	verificationID := uuid.New()
	code, err := generateCode()
	if err != nil {
		return SendResult{}, nil, fmt.Errorf("generate verification code: %w", err)
	}
	now := s.now().UTC()
	challenge := Challenge{
		ID:          verificationID,
		Phone:       phone,
		Purpose:     purpose,
		CodeHash:    s.hashCode(verificationID, phone, purpose, code),
		MaxAttempts: s.maxAttempts,
		ExpiresAt:   now.Add(s.codeTTL),
		CreatedAt:   now,
	}
	if err := s.repository.CreateChallenge(ctx, challenge, s.resendInterval); err != nil {
		return SendResult{}, nil, err
	}
	if err := s.sender.SendCode(ctx, phone, code); err != nil {
		return SendResult{}, nil, fmt.Errorf("%w: %v", ErrDeliveryFailed, err)
	}
	return SendResult{
		VerificationID: verificationID,
		ExpiresAt:      challenge.ExpiresAt,
		RetryAfter:     int64(s.resendInterval.Seconds()),
	}, nil, nil
}

func (s *Service) Confirm(ctx context.Context, input ConfirmInput) (ConfirmResult, map[string]string, error) {
	phone := NormalizePhone(input.Phone)
	purpose, ok := ParsePurpose(input.Purpose)
	details := make(map[string]string)
	if input.VerificationID == uuid.Nil {
		details["verificationId"] = "must be a valid UUID"
	}
	if !ValidPhone(phone) {
		details["phone"] = "must use E.164 format, for example +77001234567"
	}
	if !ok {
		details["purpose"] = "must be waitlist or registration"
	}
	if len(input.Code) != 6 || !digitsOnly(input.Code) {
		details["code"] = "must contain exactly 6 digits"
	}
	if len(details) > 0 {
		return ConfirmResult{}, details, nil
	}

	rawToken, tokenHash, err := newVerificationToken()
	if err != nil {
		return ConfirmResult{}, nil, fmt.Errorf("generate verification token: %w", err)
	}
	now := s.now().UTC()
	tokenExpiresAt := now.Add(s.tokenTTL)
	if err := s.repository.VerifyChallenge(
		ctx,
		input.VerificationID,
		phone,
		purpose,
		s.hashCode(input.VerificationID, phone, purpose, input.Code),
		tokenHash,
		now,
		tokenExpiresAt,
	); err != nil {
		return ConfirmResult{}, nil, err
	}
	return ConfirmResult{VerificationToken: rawToken, ExpiresAt: tokenExpiresAt}, nil, nil
}

func NormalizePhone(value string) string {
	value = strings.TrimSpace(value)
	var normalized strings.Builder
	for i, r := range value {
		switch {
		case r >= '0' && r <= '9':
			normalized.WriteRune(r)
		case r == '+' && i == 0:
			normalized.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')':
		default:
			return value
		}
	}
	return normalized.String()
}

func ValidPhone(value string) bool {
	if len(value) < 9 || len(value) > 16 || value[0] != '+' || value[1] == '0' {
		return false
	}
	return digitsOnly(value[1:])
}

func ParsePurpose(value string) (Purpose, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case string(PurposeWaitlist):
		return PurposeWaitlist, true
	case string(PurposeRegistration):
		return PurposeRegistration, true
	default:
		return "", false
	}
}

func HashVerificationToken(raw string) []byte {
	hash := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hash[:]
}

func ValidVerificationToken(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) >= 32 && len(raw) <= 128
}

func (s *Service) hashCode(id uuid.UUID, phone string, purpose Purpose, code string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = fmt.Fprintf(mac, "%s\x00%s\x00%s\x00%s", id, phone, purpose, code)
	return mac.Sum(nil)
}

func generateCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func newVerificationToken() (string, []byte, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(buffer)
	return raw, HashVerificationToken(raw), nil
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
