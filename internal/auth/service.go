package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/phoneverification"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	repository Repository
	tokens     *TokenManager
	bcryptCost int
	now        func() time.Time
}

func NewService(repository Repository, tokens *TokenManager) *Service {
	return &Service{
		repository: repository,
		tokens:     tokens,
		bcryptCost: bcrypt.DefaultCost,
		now:        time.Now,
	}
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (AuthResult, map[string]string, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Email = normalizeEmail(input.Email)
	input.Phone = phoneverification.NormalizePhone(input.Phone)
	input.VerificationToken = strings.TrimSpace(input.VerificationToken)
	details := validateRegistration(input)
	if len(details) > 0 {
		return AuthResult{}, details, nil
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), s.bcryptCost)
	if err != nil {
		return AuthResult{}, nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.repository.CreateUser(
		ctx,
		input.Email,
		string(passwordHash),
		input.Name,
		input.Phone,
		phoneverification.HashVerificationToken(input.VerificationToken),
		s.now().UTC(),
	)
	if err != nil {
		return AuthResult{}, nil, err
	}
	return s.issueSession(ctx, user, SessionMetadata{input.UserAgent, input.IPAddress})
}

func (s *Service) Login(ctx context.Context, input LoginInput) (AuthResult, map[string]string, error) {
	input.Email = normalizeEmail(input.Email)
	details := map[string]string{}
	if !validEmail(input.Email) {
		details["email"] = "must be a valid email address"
	}
	if input.Password == "" {
		details["password"] = "is required"
	}
	if len(details) > 0 {
		return AuthResult{}, details, nil
	}

	userRecord, err := s.repository.FindUserByEmail(ctx, input.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return AuthResult{}, nil, ErrInvalidCredentials
		}
		return AuthResult{}, nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(userRecord.PasswordHash), []byte(input.Password)); err != nil {
		return AuthResult{}, nil, ErrInvalidCredentials
	}
	user, err := s.repository.FindUserByID(ctx, userRecord.ID)
	if err != nil {
		return AuthResult{}, nil, err
	}
	return s.issueSession(ctx, user, SessionMetadata{input.UserAgent, input.IPAddress})
}

func (s *Service) Refresh(ctx context.Context, rawToken string, metadata SessionMetadata) (AuthResult, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || len(rawToken) > 256 {
		return AuthResult{}, ErrInvalidRefresh
	}

	newRaw, newHash, expiresAt, err := s.tokens.NewRefreshToken()
	if err != nil {
		return AuthResult{}, err
	}
	replacement := Session{
		ID:        uuid.New(),
		TokenHash: newHash,
		ExpiresAt: expiresAt,
		UserAgent: metadata.UserAgent,
		IPAddress: metadata.IPAddress,
	}
	userID, err := s.repository.RotateSession(ctx, HashRefreshToken(rawToken), replacement, s.now().UTC())
	if err != nil {
		return AuthResult{}, err
	}
	user, err := s.repository.FindUserByID(ctx, userID)
	if err != nil {
		return AuthResult{}, err
	}
	accessToken, _, err := s.tokens.NewAccessToken(user.ID, user.Role)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		AccessToken:  accessToken,
		RefreshToken: newRaw,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.tokens.AccessTTL().Seconds()),
		User:         user,
	}, nil
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || len(rawToken) > 256 {
		return ErrInvalidRefresh
	}
	return s.repository.RevokeSession(ctx, HashRefreshToken(rawToken), s.now().UTC())
}

func (s *Service) User(ctx context.Context, userID uuid.UUID) (UserView, error) {
	return s.repository.FindUserByID(ctx, userID)
}

func (s *Service) issueSession(ctx context.Context, user UserView, metadata SessionMetadata) (AuthResult, map[string]string, error) {
	accessToken, _, err := s.tokens.NewAccessToken(user.ID, user.Role)
	if err != nil {
		return AuthResult{}, nil, err
	}
	refreshToken, hash, expiresAt, err := s.tokens.NewRefreshToken()
	if err != nil {
		return AuthResult{}, nil, err
	}
	if err := s.repository.CreateSession(ctx, Session{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: expiresAt,
		UserAgent: metadata.UserAgent,
		IPAddress: metadata.IPAddress,
	}); err != nil {
		return AuthResult{}, nil, err
	}
	return AuthResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.tokens.AccessTTL().Seconds()),
		User:         user,
	}, nil, nil
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validEmail(value string) bool {
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func validateRegistration(input RegisterInput) map[string]string {
	details := map[string]string{}
	nameLength := utf8.RuneCountInString(input.Name)
	if nameLength < 2 || nameLength > 100 {
		details["name"] = "must contain between 2 and 100 characters"
	}
	if !validEmail(input.Email) {
		details["email"] = "must be a valid email address"
	}
	if utf8.RuneCountInString(input.Password) < 8 || len(input.Password) > 72 {
		details["password"] = "must contain at least 8 characters and at most 72 bytes"
	}
	if input.ConfirmPassword != "" && input.ConfirmPassword != input.Password {
		details["confirmPassword"] = "must match password"
	}
	if !input.AcceptedTerms {
		details["acceptedTerms"] = "must be accepted"
	}
	if !phoneverification.ValidPhone(input.Phone) {
		details["phone"] = "must use E.164 format, for example +77001234567"
	}
	if !phoneverification.ValidVerificationToken(input.VerificationToken) {
		details["verificationToken"] = "is required"
	}
	return details
}
