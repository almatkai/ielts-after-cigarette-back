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
	now := s.now().UTC()
	if existing, err := s.repository.FindUserByEmail(ctx, input.Email); err == nil {
		if !LeadStatus(existing.Status) {
			return AuthResult{}, nil, ErrEmailExists
		}
		// A waitlist lead with this email already exists — finish registration
		// on the same row instead of rejecting the sign-up.
		user, err := s.repository.CompleteWaitlistUser(
			ctx,
			existing.ID,
			"",
			string(passwordHash),
			input.Name,
			input.Phone,
			"",
			phoneverification.HashVerificationToken(input.VerificationToken),
			now,
		)
		if err != nil {
			return AuthResult{}, nil, err
		}
		return s.issueSession(ctx, user, SessionMetadata{input.UserAgent, input.IPAddress})
	} else if !errors.Is(err, ErrUserNotFound) {
		return AuthResult{}, nil, err
	}
	user, err := s.repository.CreateUser(
		ctx,
		input.Email,
		string(passwordHash),
		input.Name,
		input.Phone,
		phoneverification.HashVerificationToken(input.VerificationToken),
		now,
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

// GoogleLogin signs in with a Google ID token. Existing accounts (matched by
// google_sub, then by email — linking the sub on first match) just get a
// session. Waitlist leads (status WAITING/INVITED) never get a session here:
// super admins are upgraded to ADMIN in place, everyone else gets a pending
// registration token prefilled with the waitlist name and phone to finish
// signing up at /auth/google/complete. Unknown accounts are only provisioned
// for super admins (env or DB), always with the ADMIN role; regular users get
// the same pending registration token.
// The role is only ever elevated here, never demoted.
func (s *Service) GoogleLogin(ctx context.Context, input GoogleLoginInput) (GoogleLoginOutcome, error) {
	if s.verifier == nil || s.admins == nil {
		return GoogleLoginOutcome{}, fmt.Errorf("google login is not configured")
	}
	claims, err := s.verifier.Verify(ctx, strings.TrimSpace(input.GoogleToken))
	if err != nil {
		return GoogleLoginOutcome{}, ErrInvalidGoogleToken
	}
	email := normalizeEmail(claims.Email)
	if email == "" || !claims.EmailVerified || !validEmail(email) {
		return GoogleLoginOutcome{}, ErrInvalidGoogleToken
	}

	metadata := SessionMetadata{input.UserAgent, input.IPAddress}
	userRecord, err := s.repository.FindUserByGoogleSub(ctx, claims.Sub)
	switch {
	case err == nil:
		if LeadStatus(userRecord.Status) {
			return s.pendingGoogleRegistration(claims, email, &userRecord)
		}
		return s.googleSessionForUser(ctx, userRecord, metadata)
	case !errors.Is(err, ErrUserNotFound):
		return GoogleLoginOutcome{}, err
	}

	userRecord, err = s.repository.FindUserByEmail(ctx, email)
	switch {
	case err == nil:
		if LeadStatus(userRecord.Status) {
			isAdmin, err := s.admins.IsSuperAdmin(ctx, email)
			if err != nil {
				return GoogleLoginOutcome{}, err
			}
			if isAdmin {
				user, err := s.repository.UpgradeWaitlistToAdmin(
					ctx,
					userRecord.ID,
					googleDisplayName(claims.Name, email),
					claims.Sub,
					s.now().UTC(),
				)
				if err == nil {
					result, _, err := s.issueSession(ctx, user, metadata)
					if err != nil {
						return GoogleLoginOutcome{}, err
					}
					return GoogleLoginOutcome{Session: &result}, nil
				}
				if !errors.Is(err, ErrUserNotFound) {
					return GoogleLoginOutcome{}, err
				}
				// The lead finished registration in a concurrent request;
				// fall through to the normal sign-in below.
			} else {
				return s.pendingGoogleRegistration(claims, email, &userRecord)
			}
		}
		if userRecord.GoogleSub == "" {
			if err := s.repository.LinkGoogleSub(ctx, userRecord.ID, claims.Sub); err != nil {
				return GoogleLoginOutcome{}, err
			}
		}
		return s.googleSessionForUser(ctx, userRecord, metadata)
	case !errors.Is(err, ErrUserNotFound):
		return GoogleLoginOutcome{}, err
	}

	isAdmin, err := s.admins.IsSuperAdmin(ctx, email)
	if err != nil {
		return GoogleLoginOutcome{}, err
	}
	if !isAdmin {
		return s.pendingGoogleRegistration(claims, email, nil)
	}
	user, err := s.createGoogleAdmin(ctx, email, claims.Name, claims.Sub)
	if err != nil {
		return GoogleLoginOutcome{}, err
	}
	result, _, err := s.issueSession(ctx, user, metadata)
	if err != nil {
		return GoogleLoginOutcome{}, err
	}
	return GoogleLoginOutcome{Session: &result}, nil
}

// pendingGoogleRegistration builds the pending-registration outcome for a
// Google profile without a registered account. When the profile matches a
// waitlist lead row, the lead's name and phone prefill the
// complete-registration form; the session is only issued after the user
// finishes registration.
func (s *Service) pendingGoogleRegistration(claims waitlist.GoogleClaims, email string, lead *User) (GoogleLoginOutcome, error) {
	name := strings.TrimSpace(claims.Name)
	phone := ""
	if lead != nil {
		leadName := strings.TrimSpace(strings.TrimSpace(lead.FirstName) + " " + strings.TrimSpace(lead.LastName))
		if leadName != "" {
			name = leadName
		}
		phone = lead.Phone
	}
	token, err := s.tokens.NewGoogleRegistrationToken(claims.Sub, email, name)
	if err != nil {
		return GoogleLoginOutcome{}, err
	}
	return GoogleLoginOutcome{PendingRegistration: &PendingRegistration{
		Token:   token,
		Profile: GoogleProfile{Email: email, Name: googleDisplayName(name, email), Phone: phone},
	}}, nil
}

func (s *Service) googleSessionForUser(ctx context.Context, userRecord User, metadata SessionMetadata) (GoogleLoginOutcome, error) {
	user, err := s.repository.FindUserByID(ctx, userRecord.ID)
	if err != nil {
		return GoogleLoginOutcome{}, err
	}
	if user.Role != RoleAdmin {
		isAdmin, err := s.admins.IsSuperAdmin(ctx, userRecord.Email)
		if err != nil {
			return GoogleLoginOutcome{}, err
		}
		if isAdmin {
			if err := s.repository.SetRole(ctx, userRecord.Email, RoleAdmin); err != nil {
				return GoogleLoginOutcome{}, err
			}
			user, err = s.repository.FindUserByID(ctx, userRecord.ID)
			if err != nil {
				return GoogleLoginOutcome{}, err
			}
		}
	}
	result, _, err := s.issueSession(ctx, user, metadata)
	if err != nil {
		return GoogleLoginOutcome{}, err
	}
	return GoogleLoginOutcome{Session: &result}, nil
}

// CompleteGoogleRegistration turns a pending Google registration token plus
// the user-chosen name, phone and password into a full STUDENT account and a
// session. The token proves both the email and the Google identity, so no
// WhatsApp phone verification is required.
func (s *Service) CompleteGoogleRegistration(
	ctx context.Context,
	input CompleteGoogleRegistrationInput,
) (AuthResult, map[string]string, error) {
	registrationToken := strings.TrimSpace(input.RegistrationToken)
	if registrationToken == "" {
		return AuthResult{}, nil, ErrInvalidGoogleToken
	}
	claims, err := s.tokens.ParseGoogleRegistrationToken(registrationToken)
	if err != nil {
		return AuthResult{}, nil, ErrInvalidGoogleToken
	}
	email := normalizeEmail(claims.Email)
	if !validEmail(email) {
		return AuthResult{}, nil, ErrInvalidGoogleToken
	}

	input.Name = strings.TrimSpace(input.Name)
	input.Phone = phoneverification.NormalizePhone(input.Phone)
	details := validateGoogleRegistration(input)
	if len(details) > 0 {
		return AuthResult{}, details, nil
	}

	var lead *User
	if existing, err := s.repository.FindUserByEmail(ctx, email); err == nil {
		if !LeadStatus(existing.Status) {
			return AuthResult{}, nil, ErrEmailExists
		}
		lead = &existing
	} else if !errors.Is(err, ErrUserNotFound) {
		return AuthResult{}, nil, err
	}
	if lead == nil {
		if existing, err := s.repository.FindUserByGoogleSub(ctx, claims.Subject); err == nil {
			if !LeadStatus(existing.Status) {
				return AuthResult{}, nil, ErrEmailExists
			}
			lead = &existing
		} else if !errors.Is(err, ErrUserNotFound) {
			return AuthResult{}, nil, err
		}
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), s.bcryptCost)
	if err != nil {
		return AuthResult{}, nil, fmt.Errorf("hash password: %w", err)
	}
	var user UserView
	if lead != nil {
		// Finish registration on the waitlist lead row; the Google email from
		// the token replaces the lead's placeholder email when there is one.
		user, err = s.repository.CompleteWaitlistUser(
			ctx,
			lead.ID,
			email,
			string(passwordHash),
			input.Name,
			input.Phone,
			claims.Subject,
			nil,
			s.now().UTC(),
		)
	} else {
		user, err = s.repository.CreateGoogleCompletedUser(
			ctx,
			email,
			string(passwordHash),
			input.Name,
			input.Phone,
			claims.Subject,
			s.now().UTC(),
		)
	}
	if err != nil {
		return AuthResult{}, nil, err
	}
	result, _, err := s.issueSession(ctx, user, SessionMetadata{input.UserAgent, input.IPAddress})
	return result, nil, err
}

// createGoogleAdmin provisions an ADMIN account for a super admin Google
// profile. The account has no password and only ever signs in with Google.
func (s *Service) createGoogleAdmin(ctx context.Context, email, displayName, googleSub string) (UserView, error) {
	user, err := s.repository.CreateGoogleUser(
		ctx,
		email,
		"",
		googleDisplayName(displayName, email),
		RoleAdmin,
		googleSub,
		s.now().UTC(),
	)
	if errors.Is(err, ErrEmailExists) {
		// A concurrent request created the account first, or the email
		// belongs to a waitlist lead that now upgrades to admin.
		userRecord, lookupErr := s.repository.FindUserByEmail(ctx, email)
		if lookupErr != nil {
			return UserView{}, lookupErr
		}
		if LeadStatus(userRecord.Status) {
			return s.repository.UpgradeWaitlistToAdmin(
				ctx,
				userRecord.ID,
				googleDisplayName(displayName, email),
				googleSub,
				s.now().UTC(),
			)
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

// validateGoogleRegistration mirrors validateRegistration minus the email and
// verification token checks — the Google registration token proves the
// identity, so neither is collected from the user.
func validateGoogleRegistration(input CompleteGoogleRegistrationInput) map[string]string {
	details := map[string]string{}
	nameLength := utf8.RuneCountInString(input.Name)
	if nameLength < 2 || nameLength > 100 {
		details["name"] = "must contain between 2 and 100 characters"
	}
	if utf8.RuneCountInString(input.Password) < 8 || len(input.Password) > 72 {
		details["password"] = "must contain at least 8 characters and at most 72 bytes"
	}
	if !input.AcceptedTerms {
		details["acceptedTerms"] = "must be accepted"
	}
	if !phoneverification.ValidPhone(input.Phone) {
		details["phone"] = "must use E.164 format, for example +77001234567"
	}
	return details
}
