package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/waitlist"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	testPhone             = "+77001234567"
	testVerificationToken = "0123456789abcdef0123456789abcdef0123456789a"
)

func TestRegisterCreatesStudentAndNormalizesEmail(t *testing.T) {
	service, repository := testService()
	result, details, err := service.Register(context.Background(), RegisterInput{
		Name:              " Alice ",
		Email:             " Alice@Example.COM ",
		Password:          "safe-password",
		AcceptedTerms:     true,
		Phone:             testPhone,
		VerificationToken: testVerificationToken,
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("register failed: details=%v err=%v", details, err)
	}
	if result.User.Email != "alice@example.com" || result.User.Role != "STUDENT" {
		t.Fatalf("unexpected user: %+v", result.User)
	}
	if result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatal("expected both access and refresh tokens")
	}
	if len(repository.sessions) != 1 {
		t.Fatalf("expected one refresh session, got %d", len(repository.sessions))
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	service, _ := testService()
	input := RegisterInput{
		Name: "Alice", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	}
	if _, _, err := service.Register(context.Background(), input); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	if _, _, err := service.Register(context.Background(), input); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("expected duplicate email error, got %v", err)
	}
}

func TestLoginAcceptsCorrectPasswordAndRejectsWrongPassword(t *testing.T) {
	service, _ := testService()
	_, _, err := service.Register(context.Background(), RegisterInput{
		Name: "Alice", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, _, err := service.Login(context.Background(), LoginInput{
		Email: "ALICE@example.com", Password: "safe-password",
	}); err != nil {
		t.Fatalf("correct login failed: %v", err)
	}
	if _, _, err := service.Login(context.Background(), LoginInput{
		Email: "alice@example.com", Password: "wrong-password",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}

func TestRefreshRotatesTokenAndDetectsReuse(t *testing.T) {
	service, _ := testService()
	registered, _, err := service.Register(context.Background(), RegisterInput{
		Name: "Alice", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	refreshed, err := service.Refresh(context.Background(), registered.RefreshToken, SessionMetadata{})
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if refreshed.RefreshToken == registered.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := service.Refresh(context.Background(), registered.RefreshToken, SessionMetadata{}); !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("expected refresh reuse error, got %v", err)
	}
}

func TestLogoutRevokesRefreshSession(t *testing.T) {
	service, _ := testService()
	registered, _, err := service.Register(context.Background(), RegisterInput{
		Name: "Alice", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.Logout(context.Background(), registered.RefreshToken); err != nil {
		t.Fatalf("logout failed: %v", err)
	}
	if _, err := service.Refresh(context.Background(), registered.RefreshToken, SessionMetadata{}); !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("expected revoked token reuse error, got %v", err)
	}
}

func TestGoogleLoginReturnsPendingRegistrationForUnknownNonAdmin(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil {
		t.Fatalf("google login failed: %v", err)
	}
	pending := outcome.PendingRegistration
	if outcome.Session != nil || pending == nil {
		t.Fatalf("expected pending registration outcome, got %+v", outcome)
	}
	if pending.Profile.Email != "person@example.com" || pending.Profile.Name != "Test User" {
		t.Fatalf("unexpected profile: %+v", pending.Profile)
	}
	claims, err := service.tokens.ParseGoogleRegistrationToken(pending.Token)
	if err != nil {
		t.Fatalf("registration token did not verify: %v", err)
	}
	if claims.Subject != "sub-person" || claims.Email != "person@example.com" || claims.Purpose != GoogleRegistrationPurpose {
		t.Fatalf("unexpected registration claims: %+v", claims)
	}
	if lifetime := claims.ExpiresAt.Sub(claims.IssuedAt.Time); lifetime != 30*time.Minute {
		t.Fatalf("unexpected registration token lifetime: %v", lifetime)
	}
}

func TestGoogleLoginCreatesAdminForNewSuperAdmin(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "admin"})
	if err != nil {
		t.Fatalf("google login failed: %v", err)
	}
	if outcome.Session == nil {
		t.Fatal("expected session outcome")
	}
	result := outcome.Session
	if result.User.Email != "admin@example.com" || result.User.Role != RoleAdmin {
		t.Fatalf("unexpected user: %+v", result.User)
	}
	if result.User.DisplayName != "Test User" {
		t.Fatalf("unexpected display name: %q", result.User.DisplayName)
	}
	if result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatal("expected both access and refresh tokens")
	}
	if len(repository.sessions) != 1 {
		t.Fatalf("expected one refresh session, got %d", len(repository.sessions))
	}
	if repository.users["admin@example.com"].GoogleSub != "sub-admin" {
		t.Fatal("google sub was not stored on the admin account")
	}
}

func TestGoogleLoginKeepsRoleForExistingStudentAndLinksGoogleSub(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	if _, _, err := service.Register(context.Background(), RegisterInput{
		Name: "Alice", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "student"})
	if err != nil {
		t.Fatalf("google login failed: %v", err)
	}
	if outcome.Session == nil {
		t.Fatal("expected session outcome")
	}
	if outcome.Session.User.Role != RoleStudent {
		t.Fatalf("expected role to stay STUDENT, got %s", outcome.Session.User.Role)
	}
	if repository.users["alice@example.com"].GoogleSub != "sub-student" {
		t.Fatal("google sub was not linked to the existing account")
	}
}

func TestGoogleLoginElevatesExistingSuperAdmin(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	if _, _, err := service.Register(context.Background(), RegisterInput{
		Name: "Admin", Email: "admin@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "admin"})
	if err != nil {
		t.Fatalf("google login failed: %v", err)
	}
	if outcome.Session == nil {
		t.Fatal("expected session outcome")
	}
	if outcome.Session.User.Role != RoleAdmin {
		t.Fatalf("expected elevated ADMIN role, got %s", outcome.Session.User.Role)
	}
	if repository.users["admin@example.com"].Role != RoleAdmin {
		t.Fatal("role was not persisted")
	}
}

func TestGoogleLoginRejectsInvalidToken(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	if _, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "bogus"}); !errors.Is(err, ErrInvalidGoogleToken) {
		t.Fatalf("expected invalid google token, got %v", err)
	}
}

func TestCompleteGoogleRegistrationCreatesStudent(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	pending, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil || pending.PendingRegistration == nil {
		t.Fatalf("expected pending registration, got %+v err=%v", pending, err)
	}
	result, details, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: pending.PendingRegistration.Token,
		Name:              "  Person Personov  ",
		Phone:             "+7 700 123 45 67",
		Password:          "safe-password",
		AcceptedTerms:     true,
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("complete registration failed: details=%v err=%v", details, err)
	}
	if result.User.Email != "person@example.com" || result.User.Role != RoleStudent {
		t.Fatalf("unexpected user: %+v", result.User)
	}
	if result.User.DisplayName != "Person Personov" {
		t.Fatalf("unexpected display name: %q", result.User.DisplayName)
	}
	if result.User.Phone != "+77001234567" {
		t.Fatalf("phone was not normalized, got %q", result.User.Phone)
	}
	if result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatal("expected both access and refresh tokens")
	}
	if len(repository.sessions) != 1 {
		t.Fatalf("expected one refresh session, got %d", len(repository.sessions))
	}
	if repository.users["person@example.com"].GoogleSub != "sub-person" {
		t.Fatal("google sub was not stored on the new account")
	}

	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil {
		t.Fatalf("google login after registration failed: %v", err)
	}
	if outcome.Session == nil || outcome.Session.User.Email != "person@example.com" {
		t.Fatalf("expected session for the registered google user, got %+v", outcome)
	}
}

func TestCompleteGoogleRegistrationRejectsInvalidRegistrationToken(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	for _, token := range []string{"", "bogus"} {
		if _, _, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
			RegistrationToken: token, Name: "Person", Phone: testPhone, Password: "safe-password", AcceptedTerms: true,
		}); !errors.Is(err, ErrInvalidGoogleToken) {
			t.Fatalf("expected invalid google token for %q, got %v", token, err)
		}
	}
	access, _, err := service.tokens.NewAccessToken(uuid.New(), RoleStudent)
	if err != nil {
		t.Fatalf("create access token: %v", err)
	}
	if _, _, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: access, Name: "Person", Phone: testPhone, Password: "safe-password", AcceptedTerms: true,
	}); !errors.Is(err, ErrInvalidGoogleToken) {
		t.Fatalf("expected access token to be rejected, got %v", err)
	}
}

func TestCompleteGoogleRegistrationRejectsExistingEmail(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	if _, _, err := service.Register(context.Background(), RegisterInput{
		Name: "Alice", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	token, err := service.tokens.NewGoogleRegistrationToken("sub-unknown", "alice@example.com", "Alice")
	if err != nil {
		t.Fatalf("create registration token: %v", err)
	}
	if _, _, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: token, Name: "Alice", Phone: "+77001234568", Password: "safe-password", AcceptedTerms: true,
	}); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("expected email exists, got %v", err)
	}
}

func TestCompleteGoogleRegistrationRejectsDuplicatePhone(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	pending, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil || pending.PendingRegistration == nil {
		t.Fatalf("expected pending registration, got %+v err=%v", pending, err)
	}
	if _, _, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: pending.PendingRegistration.Token, Name: "Person", Phone: testPhone, Password: "safe-password", AcceptedTerms: true,
	}); err != nil {
		t.Fatalf("first completion failed: %v", err)
	}
	token, err := service.tokens.NewGoogleRegistrationToken("sub-other", "other@example.com", "Other")
	if err != nil {
		t.Fatalf("create registration token: %v", err)
	}
	if _, _, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: token, Name: "Other", Phone: testPhone, Password: "safe-password", AcceptedTerms: true,
	}); !errors.Is(err, ErrPhoneExists) {
		t.Fatalf("expected phone exists, got %v", err)
	}
}

func TestCompleteGoogleRegistrationValidatesFieldsAndKeepsToken(t *testing.T) {
	service, _ := testGoogleService("admin@example.com")
	pending, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil || pending.PendingRegistration == nil {
		t.Fatalf("expected pending registration, got %+v err=%v", pending, err)
	}
	_, details, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: pending.PendingRegistration.Token,
		Name:              "A",
		Phone:             "7001234567",
		Password:          "short",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, field := range []string{"name", "phone", "password", "acceptedTerms"} {
		if details[field] == "" {
			t.Fatalf("expected validation detail for %s, got %v", field, details)
		}
	}
	if _, details, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: pending.PendingRegistration.Token,
		Name:              "Person", Phone: testPhone, Password: "safe-password", AcceptedTerms: true,
	}); err != nil || len(details) != 0 {
		t.Fatalf("registration token should survive a failed attempt: details=%v err=%v", details, err)
	}
}

func TestGoogleLoginReturnsPendingForWaitlistLead(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	repository.seedLead("person@example.com", "+77009998877", "Алмат", "Кайратов", "")

	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil {
		t.Fatalf("google login failed: %v", err)
	}
	pending := outcome.PendingRegistration
	if outcome.Session != nil || pending == nil {
		t.Fatalf("expected pending registration outcome, got %+v", outcome)
	}
	if pending.Profile.Email != "person@example.com" {
		t.Fatalf("unexpected email: %q", pending.Profile.Email)
	}
	if pending.Profile.Name != "Алмат Кайратов" {
		t.Fatalf("expected waitlist name to prefill the form, got %q", pending.Profile.Name)
	}
	if pending.Profile.Phone != "+77009998877" {
		t.Fatalf("expected waitlist phone to prefill the form, got %q", pending.Profile.Phone)
	}
}

func TestGoogleLoginReturnsPendingForWaitlistLeadByGoogleSub(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	repository.seedLead("waitlist-orphan@placeholder.invalid", "+77009998877", "Aidos", "Serik", "sub-person")

	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil {
		t.Fatalf("google login failed: %v", err)
	}
	pending := outcome.PendingRegistration
	if outcome.Session != nil || pending == nil {
		t.Fatalf("expected pending registration outcome, got %+v", outcome)
	}
	if pending.Profile.Email != "person@example.com" {
		t.Fatalf("expected the google email, not the placeholder, got %q", pending.Profile.Email)
	}
	if pending.Profile.Name != "Aidos Serik" || pending.Profile.Phone != "+77009998877" {
		t.Fatalf("unexpected profile: %+v", pending.Profile)
	}
}

func TestCompleteGoogleRegistrationCompletesWaitlistLead(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	lead := repository.seedLead("person@example.com", "+77009998877", "Aidos", "Serik", "")

	pending, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil || pending.PendingRegistration == nil {
		t.Fatalf("expected pending registration, got %+v err=%v", pending, err)
	}
	result, details, err := service.CompleteGoogleRegistration(context.Background(), CompleteGoogleRegistrationInput{
		RegistrationToken: pending.PendingRegistration.Token,
		Name:              "Aidos Serik",
		Phone:             "+7 700 123 45 67",
		Password:          "safe-password",
		AcceptedTerms:     true,
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("complete registration failed: details=%v err=%v", details, err)
	}
	if result.User.ID != lead.ID {
		t.Fatalf("expected the waitlist row to be reused, got %v want %v", result.User.ID, lead.ID)
	}
	if result.User.Role != RoleStudent || result.User.Phone != "+77001234567" {
		t.Fatalf("unexpected user: %+v", result.User)
	}
	if repository.users["person@example.com"].Status != StatusRegistered {
		t.Fatal("lead row was not marked REGISTERED")
	}
	if len(repository.sessions) != 1 {
		t.Fatalf("expected one refresh session, got %d", len(repository.sessions))
	}

	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "person"})
	if err != nil {
		t.Fatalf("google login after registration failed: %v", err)
	}
	if outcome.Session == nil || outcome.Session.User.ID != lead.ID {
		t.Fatalf("expected session for the completed lead, got %+v", outcome)
	}
	if _, _, err := service.Login(context.Background(), LoginInput{
		Email: "person@example.com", Password: "safe-password",
	}); err != nil {
		t.Fatalf("password login after completion failed: %v", err)
	}
}

func TestRegisterCompletesWaitlistLead(t *testing.T) {
	service, repository := testService()
	lead := repository.seedLead("alice@example.com", testPhone, "Alice", "Example", "")

	result, _, err := service.Register(context.Background(), RegisterInput{
		Name: "Alice Example", Email: "alice@example.com", Password: "safe-password", AcceptedTerms: true,
		Phone: testPhone, VerificationToken: testVerificationToken,
	})
	if err != nil {
		t.Fatalf("register over a waitlist lead failed: %v", err)
	}
	if result.User.ID != lead.ID {
		t.Fatalf("expected the waitlist row to be reused, got %v want %v", result.User.ID, lead.ID)
	}
	if repository.users["alice@example.com"].Status != StatusRegistered {
		t.Fatal("lead row was not marked REGISTERED")
	}
	if _, _, err := service.Login(context.Background(), LoginInput{
		Email: "alice@example.com", Password: "safe-password",
	}); err != nil {
		t.Fatalf("password login after registration failed: %v", err)
	}
}

func TestLoginRejectsWaitlistLead(t *testing.T) {
	service, repository := testService()
	repository.seedLead("alice@example.com", testPhone, "Alice", "Example", "")

	if _, _, err := service.Login(context.Background(), LoginInput{
		Email: "alice@example.com", Password: "safe-password",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials for a waitlist lead, got %v", err)
	}
}

func TestGoogleLoginUpgradesWaitlistLeadToAdminForSuperAdmin(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	lead := repository.seedLead("admin@example.com", "+77009998877", "Boss", "Adminov", "")

	outcome, err := service.GoogleLogin(context.Background(), GoogleLoginInput{GoogleToken: "admin"})
	if err != nil {
		t.Fatalf("google login failed: %v", err)
	}
	if outcome.Session == nil {
		t.Fatal("expected session outcome")
	}
	if outcome.Session.User.ID != lead.ID || outcome.Session.User.Role != RoleAdmin {
		t.Fatalf("unexpected user: %+v", outcome.Session.User)
	}
	upgraded := repository.users["admin@example.com"]
	if upgraded.Status != StatusRegistered || upgraded.Role != RoleAdmin {
		t.Fatalf("lead row was not upgraded: %+v", upgraded)
	}
	if upgraded.GoogleSub != "sub-admin" {
		t.Fatal("google sub was not stored on the upgraded account")
	}
}

type fakeGoogleVerifier struct {
	emails map[string]string
}

func (v fakeGoogleVerifier) Verify(_ context.Context, token string) (waitlist.GoogleClaims, error) {
	email, ok := v.emails[token]
	if !ok {
		return waitlist.GoogleClaims{}, errors.New("bad token")
	}
	return waitlist.GoogleClaims{Sub: "sub-" + token, Email: email, EmailVerified: true, Name: "Test User"}, nil
}

type fakeSuperAdminChecker struct {
	emails map[string]struct{}
}

func (c fakeSuperAdminChecker) IsSuperAdmin(_ context.Context, email string) (bool, error) {
	_, ok := c.emails[email]
	return ok, nil
}

func testGoogleService(adminEmail string) (*Service, *fakeRepository) {
	service, repository := testService()
	verifier := fakeGoogleVerifier{emails: map[string]string{
		"admin":   "admin@example.com",
		"student": "alice@example.com",
		"person":  "person@example.com",
	}}
	service.WithGoogleLogin(verifier, fakeSuperAdminChecker{emails: map[string]struct{}{adminEmail: {}}})
	return service, repository
}

func TestAuthenticateProtectsEndpoint(t *testing.T) {
	tokens := NewTokenManager(
		"0123456789abcdef0123456789abcdef",
		"issuer",
		"audience",
		time.Minute,
		time.Hour,
	)
	userID := uuid.New()
	token, _, err := tokens.NewAccessToken(userID, "STUDENT")
	if err != nil {
		t.Fatalf("create access token: %v", err)
	}
	handler := Authenticate(tokens)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual, ok := UserID(r.Context())
		if !ok || actual != userID {
			t.Fatalf("unexpected authenticated user: %v %v", actual, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized response, got %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("expected protected endpoint success, got %d", authorized.Code)
	}
}

type fakeRepository struct {
	mu       sync.Mutex
	users    map[string]User
	views    map[uuid.UUID]UserView
	sessions map[string]fakeSession
}

type fakeSession struct {
	session Session
	revoked bool
}

func testService() (*Service, *fakeRepository) {
	repository := &fakeRepository{
		users:    map[string]User{},
		views:    map[uuid.UUID]UserView{},
		sessions: map[string]fakeSession{},
	}
	tokens := NewTokenManager(
		"0123456789abcdef0123456789abcdef",
		"issuer",
		"audience",
		time.Minute,
		time.Hour,
	)
	service := NewService(repository, tokens)
	service.bcryptCost = bcrypt.MinCost
	return service, repository
}

func (r *fakeRepository) CreateUser(
	_ context.Context,
	email, hash, displayName, phone string,
	_ []byte,
	now time.Time,
) (UserView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.users[email]; exists {
		return UserView{}, ErrEmailExists
	}
	user := User{ID: uuid.New(), Email: email, PasswordHash: hash, Role: "STUDENT", Status: StatusRegistered, Phone: phone}
	view := UserView{
		ID: user.ID, Email: email, Phone: phone, DisplayName: displayName, Role: user.Role,
		Timezone: "UTC", CreatedAt: now, UpdatedAt: now,
	}
	r.users[email] = user
	r.views[user.ID] = view
	return view, nil
}

func (r *fakeRepository) CreateGoogleUser(
	_ context.Context,
	email, hash, displayName, role, googleSub string,
	now time.Time,
) (UserView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.users[email]; exists {
		return UserView{}, ErrEmailExists
	}
	user := User{ID: uuid.New(), Email: email, PasswordHash: hash, Role: role, GoogleSub: googleSub, Status: StatusRegistered}
	view := UserView{
		ID: user.ID, Email: email, DisplayName: displayName, Role: role,
		Timezone: "UTC", CreatedAt: now, UpdatedAt: now,
	}
	r.users[email] = user
	r.views[user.ID] = view
	return view, nil
}

func (r *fakeRepository) CreateGoogleCompletedUser(
	_ context.Context,
	email, hash, displayName, phone, googleSub string,
	now time.Time,
) (UserView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.users[email]; exists {
		return UserView{}, ErrEmailExists
	}
	for _, view := range r.views {
		if phone != "" && view.Phone == phone {
			return UserView{}, ErrPhoneExists
		}
	}
	user := User{ID: uuid.New(), Email: email, PasswordHash: hash, Role: "STUDENT", GoogleSub: googleSub, Status: StatusRegistered, Phone: phone}
	view := UserView{
		ID: user.ID, Email: email, Phone: phone, DisplayName: displayName, Role: user.Role,
		Timezone: "UTC", CreatedAt: now, UpdatedAt: now,
	}
	r.users[email] = user
	r.views[user.ID] = view
	return view, nil
}

func (r *fakeRepository) CompleteWaitlistUser(
	_ context.Context,
	userID uuid.UUID,
	email, hash, displayName, phone, googleSub string,
	_ []byte,
	now time.Time,
) (UserView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := ""
	var user User
	found := false
	for candidate, entry := range r.users {
		if entry.ID == userID {
			key, user, found = candidate, entry, true
			break
		}
	}
	if !found || !LeadStatus(user.Status) {
		return UserView{}, ErrUserNotFound
	}
	if phone != "" {
		for id, view := range r.views {
			if id != userID && view.Phone == phone {
				return UserView{}, ErrPhoneExists
			}
		}
	}
	if email != "" && email != key {
		if _, exists := r.users[email]; exists {
			return UserView{}, ErrEmailExists
		}
		delete(r.users, key)
		user.Email = email
		key = email
	}
	user.PasswordHash = hash
	user.Status = StatusRegistered
	if user.GoogleSub == "" {
		user.GoogleSub = googleSub
	}
	if phone != "" {
		user.Phone = phone
	}
	r.users[key] = user
	view := r.views[user.ID]
	view.Email = user.Email
	if phone != "" {
		view.Phone = phone
	}
	if displayName != "" {
		view.DisplayName = displayName
	}
	view.Role = user.Role
	view.UpdatedAt = now
	if view.Timezone == "" {
		view.Timezone = "UTC"
	}
	r.views[user.ID] = view
	return view, nil
}

func (r *fakeRepository) UpgradeWaitlistToAdmin(
	_ context.Context,
	userID uuid.UUID,
	displayName, googleSub string,
	now time.Time,
) (UserView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, user := range r.users {
		if user.ID != userID {
			continue
		}
		if !LeadStatus(user.Status) {
			return UserView{}, ErrUserNotFound
		}
		user.Role = RoleAdmin
		user.Status = StatusRegistered
		if user.GoogleSub == "" {
			user.GoogleSub = googleSub
		}
		r.users[key] = user
		view := r.views[user.ID]
		if displayName != "" {
			view.DisplayName = displayName
		}
		view.Role = RoleAdmin
		view.UpdatedAt = now
		if view.Timezone == "" {
			view.Timezone = "UTC"
		}
		r.views[user.ID] = view
		return view, nil
	}
	return UserView{}, ErrUserNotFound
}

// seedLead inserts a waitlist lead row (status WAITING) directly, mirroring
// what the waitlist repository writes when someone joins the waitlist.
func (r *fakeRepository) seedLead(email, phone, firstName, lastName, googleSub string) User {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	user := User{
		ID:        uuid.New(),
		Email:     email,
		Role:      RoleStudent,
		GoogleSub: googleSub,
		Status:    StatusWaiting,
		Phone:     phone,
		FirstName: firstName,
		LastName:  lastName,
	}
	r.users[email] = user
	r.views[user.ID] = UserView{
		ID: user.ID, Email: email, Phone: phone, Role: RoleStudent,
		Timezone: "UTC", CreatedAt: now, UpdatedAt: now,
	}
	return user
}

func (r *fakeRepository) LinkGoogleSub(_ context.Context, userID uuid.UUID, googleSub string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for email, user := range r.users {
		if user.ID != userID {
			continue
		}
		if user.GoogleSub == "" {
			user.GoogleSub = googleSub
			r.users[email] = user
		}
		return nil
	}
	return ErrUserNotFound
}

func (r *fakeRepository) FindUserByGoogleSub(_ context.Context, googleSub string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, user := range r.users {
		if user.GoogleSub != "" && user.GoogleSub == googleSub {
			return user, nil
		}
	}
	return User{}, ErrUserNotFound
}

func (r *fakeRepository) SetRole(_ context.Context, email, role string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.users[email]
	if !ok {
		return ErrUserNotFound
	}
	user.Role = role
	r.users[email] = user
	view := r.views[user.ID]
	view.Role = role
	r.views[user.ID] = view
	return nil
}

func (r *fakeRepository) FindUserByEmail(_ context.Context, email string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.users[email]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (r *fakeRepository) FindUserByID(_ context.Context, userID uuid.UUID) (UserView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.views[userID]
	if !ok {
		return UserView{}, ErrUserNotFound
	}
	return user, nil
}

func (r *fakeRepository) CreateSession(_ context.Context, session Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[string(session.TokenHash)] = fakeSession{session: session}
	return nil
}

func (r *fakeRepository) RotateSession(
	_ context.Context,
	oldHash []byte,
	replacement Session,
	now time.Time,
) (uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.sessions[string(oldHash)]
	if !ok || !current.session.ExpiresAt.After(now) {
		return uuid.Nil, ErrInvalidRefresh
	}
	if current.revoked {
		for key, candidate := range r.sessions {
			if candidate.session.UserID == current.session.UserID {
				candidate.revoked = true
				r.sessions[key] = candidate
			}
		}
		return uuid.Nil, ErrRefreshReuse
	}
	current.revoked = true
	r.sessions[string(oldHash)] = current
	replacement.UserID = current.session.UserID
	r.sessions[string(replacement.TokenHash)] = fakeSession{session: replacement}
	return current.session.UserID, nil
}

func (r *fakeRepository) RevokeSession(_ context.Context, hash []byte, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[string(hash)]
	if !ok {
		return ErrInvalidRefresh
	}
	session.revoked = true
	r.sessions[string(hash)] = session
	return nil
}
