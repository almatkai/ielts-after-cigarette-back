package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/phoneverification"
	"github.com/almatkai/ielts-after-cigarette-back/internal/waitlist"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type GoogleClaimsVerifier interface {
	Verify(ctx context.Context, idToken string) (waitlist.GoogleClaims, error)
}

type SuperAdminChecker interface {
	IsSuperAdmin(ctx context.Context, email string) (bool, error)
}

type Service struct {
	repository Repository
	tokens     *TokenManager
	verifier   GoogleClaimsVerifier
	admins     SuperAdminChecker
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

// WithGoogleLogin enables Google sign-in on the service. The verifier validates
// Google ID tokens; the checker decides which Google accounts may bootstrap a
// platform account (super admins only).
func (s *Service) WithGoogleLogin(verifier GoogleClaimsVerifier, admins SuperAdminChecker) *Service {
	s.verifier = verifier
	s.admins = admins
	return s
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

// GoogleLogin signs in with a Google ID token. Existing accounts just get a
// session. Unknown accounts are only provisioned for super admins (env or DB),
// and always with the ADMIN role — regular students register with a password.
// The role is only ever elevated here, never demoted.
func (s *Service) GoogleLogin(ctx context.Context, input GoogleLoginInput) (AuthResult, error) {
	if s.verifier == nil || s.admins == nil {
		return AuthResult{}, fmt.Errorf("google login is not configured")
	}
	claims, err := s.verifier.Verify(ctx, strings.TrimSpace(input.GoogleToken))
	if err != nil {
		return AuthResult{}, ErrInvalidGoogleToken
	}
	email := normalizeEmail(claims.Email)
	if email == "" || !claims.EmailVerified || !validEmail(email) {
		return AuthResult{}, ErrInvalidGoogleToken
	}

	metadata := SessionMetadata{input.UserAgent, input.IPAddress}
	userRecord, err := s.repository.FindUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return AuthResult{}, err
	}
	if err == nil {
		user, err := s.repository.FindUserByID(ctx, userRecord.ID)
		if err != nil {
			return AuthResult{}, err
		}
		if user.Role != RoleAdmin {
			isAdmin, err := s.admins.IsSuperAdmin(ctx, email)
			if err != nil {
				return AuthResult{}, err
			}
			if isAdmin {
				if err := s.repository.SetRole(ctx, email, RoleAdmin); err != nil {
					return AuthResult{}, err
				}
				user, err = s.repository.FindUserByID(ctx, userRecord.ID)
				if err != nil {
					return AuthResult{}, err
				}
			}
		}
		result, _, err := s.issueSession(ctx, user, metadata)
		return result, err
	}

	isAdmin, err := s.admins.IsSuperAdmin(ctx, email)
	if err != nil {
		return AuthResult{}, err
	}
	if !isAdmin {
		return AuthResult{}, ErrAccountNotFound
	}
	user, err := s.createGoogleAdmin(ctx, email, claims.Name)
	if err != nil {
		return AuthResult{}, err
	}
	result, _, err := s.issueSession(ctx, user, metadata)
	return result, err
}

// createGoogleAdmin provisions an ADMIN account for a super admin Google
// profile. The password is random and unknown to anyone; the account only ever
// signs in with Google.
func (s *Service) createGoogleAdmin(ctx context.Context, email, displayName string) (UserView, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return UserView{}, fmt.Errorf("generate google account password: %w", err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(random)), s.bcryptCost)
	if err != nil {
		return UserView{}, fmt.Errorf("hash google account password: %w", err)
	}
	user, err := s.repository.CreateGoogleUser(
		ctx,
		email,
		string(passwordHash),
		googleDisplayName(displayName, email),
		RoleAdmin,
		s.now().UTC(),
	)
	if errors.Is(err, ErrEmailExists) {
		// A concurrent request created the account first; sign in to it.
		userRecord, lookupErr := s.repository.FindUserByEmail(ctx, email)
		if lookupErr != nil {
			return UserView{}, lookupErr
		}
		return s.repository.FindUserByID(ctx, userRecord.ID)
	}
	return user, err
}

// googleDisplayName prefers the Google profile name and falls back to the
// email local part, clamped to the user_profiles constraints (1-100 chars).
func googleDisplayName(name, email string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(strings.SplitN(email, "@", 2)[0])
	}
	if name == "" {
		name = "Admin"
	}
	if utf8.RuneCountInString(name) > 100 {
		runes := []rune(name)
		name = string(runes[:100])
	}
	return name
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
