package waitlist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validGoogleClaims() GoogleClaims {
	return GoogleClaims{
		Sub:           "google-sub-123",
		Email:         " Ada@Google.com ",
		EmailVerified: true,
	}
}

func TestJoinNormalizesWaitlistEntry(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()})
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	entry, details, err := service.Join(context.Background(), JoinInput{
		FirstName:   " Ada ",
		LastName:    " Lovelace ",
		Phone:       "+7 (700) 123-45-67",
		Source:      " landing ",
		GoogleToken: " valid-token ",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if repository.params.FirstName != "Ada" || repository.params.LastName != "Lovelace" ||
		repository.params.Phone != "+77001234567" || repository.params.Source != "landing" {
		t.Fatalf("unexpected params: %+v", repository.params)
	}
	if !entry.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt=%v, want %v", entry.CreatedAt, now)
	}
}

func TestJoinRequiresNamesAndValidPhone(t *testing.T) {
	service := NewService(&fakeRepository{}, &fakeVerifier{claims: validGoogleClaims()})
	_, details, err := service.Join(context.Background(), JoinInput{GoogleToken: "valid-token"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"firstName", "lastName", "phone"} {
		if details[field] == "" {
			t.Fatalf("missing %q detail: %v", field, details)
		}
	}
}

func TestJoinRejectsInvalidPhone(t *testing.T) {
	service := NewService(&fakeRepository{}, &fakeVerifier{claims: validGoogleClaims()})
	_, details, err := service.Join(context.Background(), JoinInput{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Phone:       "12345",
		GoogleToken: "valid-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if details["phone"] == "" {
		t.Fatalf("missing phone detail: %v", details)
	}
}

func TestJoinRequiresGoogleToken(t *testing.T) {
	repository := &fakeRepository{}
	verifier := &fakeVerifier{claims: validGoogleClaims()}
	service := NewService(repository, verifier)

	_, details, err := service.Join(context.Background(), JoinInput{
		FirstName: "Ada",
		LastName:  "Lovelace",
		Email:     "ada@example.com",
		Phone:     "+77001234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	if details["googleToken"] != "is required" {
		t.Fatalf("details=%v, want googleToken is required", details)
	}
	if verifier.calls != 0 {
		t.Fatalf("Verify() called %d times, want 0", verifier.calls)
	}
	if repository.called {
		t.Fatal("repository.Create must not be called without a google token")
	}
}

func TestJoinWithInvalidGoogleTokenReturnsValidationError(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{err: errors.New("bad token")})

	_, details, err := service.Join(context.Background(), JoinInput{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Phone:       "+77001234567",
		GoogleToken: "bogus",
	})
	if err != nil {
		t.Fatal(err)
	}
	if details["googleToken"] != "is invalid" {
		t.Fatalf("details=%v, want googleToken is invalid", details)
	}
	if repository.called {
		t.Fatal("repository.Create must not be called for an invalid google token")
	}
}

func TestJoinWithValidGoogleTokenStoresSubAndClaimEmail(t *testing.T) {
	repository := &fakeRepository{}
	verifier := &fakeVerifier{claims: validGoogleClaims()}
	service := NewService(repository, verifier)

	entry, details, err := service.Join(context.Background(), JoinInput{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Email:       "typed@example.com",
		Phone:       "+77001234567",
		Source:      "landing",
		GoogleToken: "valid-token",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if verifier.calls != 1 {
		t.Fatalf("Verify() called %d times, want 1", verifier.calls)
	}
	params := repository.params
	if params.FirstName != "Ada" || params.LastName != "Lovelace" ||
		params.Phone != "+77001234567" || params.Source != "landing" ||
		params.GoogleSub != "google-sub-123" {
		t.Fatalf("unexpected params: %+v", params)
	}
	if params.Email != "ada@google.com" {
		t.Fatalf("Email=%q, want verified claims email", params.Email)
	}
	if entry.GoogleSub == nil || *entry.GoogleSub != "google-sub-123" {
		t.Fatalf("entry.GoogleSub=%v, want google-sub-123", entry.GoogleSub)
	}
	if entry.Email == nil || *entry.Email != "ada@google.com" {
		t.Fatalf("entry.Email=%v, want ada@google.com", entry.Email)
	}
	if entry.PhoneVerifiedAt != nil {
		t.Fatalf("PhoneVerifiedAt=%v, want nil", entry.PhoneVerifiedAt)
	}
}

func TestJoinWithoutVerifiedClaimsEmailReturnsValidationError(t *testing.T) {
	claimsCases := map[string]GoogleClaims{
		"email not verified": {Sub: "google-sub-123", Email: "ada@google.com", EmailVerified: false},
		"email missing":      {Sub: "google-sub-123", EmailVerified: true},
	}
	for name, claims := range claimsCases {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository, &fakeVerifier{claims: claims})

			_, details, err := service.Join(context.Background(), JoinInput{
				FirstName:   "Ada",
				LastName:    "Lovelace",
				Email:       "ada@example.com",
				Phone:       "+77001234567",
				GoogleToken: "valid-token",
			})
			if err != nil {
				t.Fatal(err)
			}
			if details["googleToken"] != "does not provide a verified email" {
				t.Fatalf("details=%v, want googleToken verified email detail", details)
			}
			if repository.called {
				t.Fatal("repository.Create must not be called without a verified claims email")
			}
		})
	}
}

type fakeVerifier struct {
	claims GoogleClaims
	err    error
	calls  int
}

func (v *fakeVerifier) Verify(_ context.Context, _ string) (GoogleClaims, error) {
	v.calls++
	return v.claims, v.err
}

type fakeRepository struct {
	params CreateParams
	called bool
}

func (r *fakeRepository) Create(_ context.Context, params CreateParams) (Entry, error) {
	r.params = params
	r.called = true
	entry := Entry{
		ID:        uuid.New(),
		Phone:     params.Phone,
		Status:    "WAITING",
		CreatedAt: params.CreatedAt,
	}
	if params.Email != "" {
		entry.Email = &params.Email
	}
	if params.GoogleSub != "" {
		entry.GoogleSub = &params.GoogleSub
	}
	return entry, nil
}
