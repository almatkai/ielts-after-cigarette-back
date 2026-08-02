package waitlist

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJoinNormalizesWaitlistEntry(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	entry, details, err := service.Join(context.Background(), JoinInput{
		Name:              " Ada Lovelace ",
		Email:             " ADA@Example.COM ",
		Phone:             "+7 (700) 123-45-67",
		Source:            " landing ",
		VerificationToken: "0123456789abcdef0123456789abcdef0123456789a",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if repository.params.Name != "Ada Lovelace" || repository.params.Email != "ada@example.com" ||
		repository.params.Phone != "+77001234567" || repository.params.Source != "landing" {
		t.Fatalf("unexpected params: %+v", repository.params)
	}
	if !entry.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt=%v, want %v", entry.CreatedAt, now)
	}
}

func TestJoinRequiresVerifiedPhoneToken(t *testing.T) {
	service := NewService(&fakeRepository{})
	_, details, err := service.Join(context.Background(), JoinInput{Phone: "+77001234567"})
	if err != nil {
		t.Fatal(err)
	}
	if details["verificationToken"] == "" {
		t.Fatalf("missing verification token detail: %v", details)
	}
}

type fakeRepository struct {
	params CreateParams
}

func (r *fakeRepository) Create(_ context.Context, params CreateParams) (Entry, error) {
	r.params = params
	return Entry{
		ID:              uuid.New(),
		Phone:           params.Phone,
		Status:          "WAITING",
		PhoneVerifiedAt: params.CreatedAt,
		CreatedAt:       params.CreatedAt,
	}, nil
}
