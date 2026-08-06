package waitlist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJoinNormalizesWaitlistEntry(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{})
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	entry, details, err := service.Join(context.Background(), JoinInput{
		FirstName:         " Ada ",
		LastName:          " Lovelace ",
		Email:             " ADA@Example.COM ",
		Phone:             "+7 (700) 123-45-67",
		Source:            " landing ",
		VerificationToken: "0123456789abcdef0123456789abcdef0123456789a",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if repository.params.FirstName != "Ada" || repository.params.LastName != "Lovelace" ||
		repository.params.Email != "ada@example.com" ||
		repository.params.Phone != "+77001234567" || repository.params.Source != "landing" {
		t.Fatalf("unexpected params: %+v", repository.params)
	}
	if !entry.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt=%v, want %v", entry.CreatedAt, now)
	}
}

func TestJoinRequiresNamesAndEmail(t *testing.T) {
	service := NewService(&fakeRepository{}, &fakeVerifier{})
	_, details, err := service.Join(context.Background(), JoinInput{Phone: "+77001234567"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"firstName", "lastName", "email", "verificationToken"} {
		if details[field] == "" {
			t.Fatalf("missing %q detail: %v", field, details)
		}
	}
}

func TestJoinRequiresVerifiedPhoneToken(t *testing.T) {
	service := NewService(&fakeRepository{}, &fakeVerifier{})
	_, details, err := service.Join(context.Background(), JoinInput{Phone: "+77001234567"})
	if err != nil {
		t.Fatal(err)
	}
	if details["verificationToken"] == "" {
		t.Fatalf("missing verification token detail: %v", details)
	}
}

func TestJoinWithValidGoogleTokenStoresSubAndClaimEmail(t *testing.T) {
	repository := &fakeRepository{}
	verifier := &fakeVerifier{
		claims: GoogleClaims{
			Sub:           "google-sub-123",
			Email:         " Ada@Google.com ",
			EmailVerified: true,
		},
	}
	service := NewService(repository, verifier)

	_, details, err := service.Join(context.Background(), JoinInput{
		FirstName:         "Ada",
		LastName:          "Lovelace",
		Email:             "typed@example.com",
		Phone:             "+77001234567",
		VerificationToken: "0123456789abcdef0123456789abcdef0123456789a",
		GoogleToken:       " valid-token ",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if verifier.calls != 1 {
		t.Fatalf("Verify() called %d times, want 1", verifier.calls)
	}
	if repository.params.GoogleSub != "google-sub-123" {
		t.Fatalf("GoogleSub=%q, want %q", repository.params.GoogleSub, "google-sub-123")
	}
	if repository.params.Email != "ada@google.com" {
		t.Fatalf("Email=%q, want verified claims email", repository.params.Email)
	}
}

func TestJoinWithInvalidGoogleTokenReturnsValidationError(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{err: errors.New("bad token")})

	_, details, err := service.Join(context.Background(), JoinInput{
		FirstName:         "Ada",
		LastName:          "Lovelace",
		Email:             "ada@example.com",
		Phone:             "+77001234567",
		VerificationToken: "0123456789abcdef0123456789abcdef0123456789a",
		GoogleToken:       "bogus",
	})
	if err != nil {
		t.Fatal(err)
	}
	if details["googleToken"] != "is invalid" {
		t.Fatalf("details=%v, want googleToken detail", details)
	}
	if repository.called {
		t.Fatal("repository.Create must not be called for an invalid google token")
	}
}

func TestJoinWithoutGoogleTokenSkipsVerification(t *testing.T) {
	repository := &fakeRepository{}
	verifier := &fakeVerifier{err: errors.New("must not be called")}
	service := NewService(repository, verifier)

	_, details, err := service.Join(context.Background(), JoinInput{
		FirstName:         "Ada",
		LastName:          "Lovelace",
		Email:             "ada@example.com",
		Phone:             "+77001234567",
		VerificationToken: "0123456789abcdef0123456789abcdef0123456789a",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if verifier.calls != 0 {
		t.Fatalf("Verify() called %d times, want 0", verifier.calls)
	}
	if repository.params.GoogleSub != "" {
		t.Fatalf("GoogleSub=%q, want empty", repository.params.GoogleSub)
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
	return Entry{
		ID:              uuid.New(),
		Phone:           params.Phone,
		Status:          "WAITING",
		PhoneVerifiedAt: params.CreatedAt,
		CreatedAt:       params.CreatedAt,
	}, nil
}
