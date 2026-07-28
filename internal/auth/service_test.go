package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func TestRegisterCreatesStudentAndNormalizesEmail(t *testing.T) {
	service, repository := testService()
	result, details, err := service.Register(context.Background(), RegisterInput{
		Name:          " Alice ",
		Email:         " Alice@Example.COM ",
		Password:      "safe-password",
		AcceptedTerms: true,
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
	email, hash, displayName string,
	now time.Time,
) (UserView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.users[email]; exists {
		return UserView{}, ErrEmailExists
	}
	user := User{ID: uuid.New(), Email: email, PasswordHash: hash, Role: "STUDENT"}
	view := UserView{
		ID: user.ID, Email: email, DisplayName: displayName, Role: user.Role,
		Timezone: "UTC", CreatedAt: now, UpdatedAt: now,
	}
	r.users[email] = user
	r.views[user.ID] = view
	return view, nil
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
