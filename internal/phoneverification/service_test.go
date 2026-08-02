package phoneverification

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSendAndConfirmPhoneVerification(t *testing.T) {
	repository := &fakeRepository{}
	sender := &fakeSender{}
	service := NewService(
		repository,
		sender,
		"0123456789abcdef0123456789abcdef",
		5*time.Minute,
		10*time.Minute,
		time.Minute,
		5,
	)
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	sent, details, err := service.Send(context.Background(), SendInput{
		Phone: "+7 (700) 123-45-67", Purpose: "registration",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Send() details=%v err=%v", details, err)
	}
	if sender.phone != "+77001234567" || len(sender.code) != 6 || !digitsOnly(sender.code) {
		t.Fatalf("unexpected WhatsApp payload: phone=%q code=%q", sender.phone, sender.code)
	}
	if sent.VerificationID != repository.challenge.ID || !sent.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("unexpected send result: %+v", sent)
	}

	confirmed, details, err := service.Confirm(context.Background(), ConfirmInput{
		VerificationID: sent.VerificationID,
		Phone:          "+77001234567",
		Purpose:        "REGISTRATION",
		Code:           sender.code,
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Confirm() details=%v err=%v", details, err)
	}
	if !ValidVerificationToken(confirmed.VerificationToken) {
		t.Fatalf("invalid verification token: %q", confirmed.VerificationToken)
	}
	if !confirmed.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("token expiry=%v, want %v", confirmed.ExpiresAt, now.Add(10*time.Minute))
	}
}

func TestConfirmRejectsWrongCode(t *testing.T) {
	repository := &fakeRepository{}
	sender := &fakeSender{}
	service := NewService(
		repository,
		sender,
		"0123456789abcdef0123456789abcdef",
		5*time.Minute,
		10*time.Minute,
		time.Minute,
		5,
	)
	sent, _, err := service.Send(context.Background(), SendInput{
		Phone: "+77001234567", Purpose: "waitlist",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Confirm(context.Background(), ConfirmInput{
		VerificationID: sent.VerificationID,
		Phone:          "+77001234567",
		Purpose:        "waitlist",
		Code:           "999999",
	})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Confirm() error=%v, want ErrInvalidCode", err)
	}
}

func TestSendValidatesPhoneAndPurpose(t *testing.T) {
	service := NewService(
		&fakeRepository{},
		&fakeSender{},
		"0123456789abcdef0123456789abcdef",
		5*time.Minute,
		10*time.Minute,
		time.Minute,
		5,
	)
	_, details, err := service.Send(context.Background(), SendInput{
		Phone: "07001234567", Purpose: "login",
	})
	if err != nil {
		t.Fatal(err)
	}
	if details["phone"] == "" || details["purpose"] == "" {
		t.Fatalf("missing validation details: %v", details)
	}
}

type fakeRepository struct {
	challenge Challenge
}

func (r *fakeRepository) CreateChallenge(_ context.Context, challenge Challenge, _ time.Duration) error {
	r.challenge = challenge
	return nil
}

func (r *fakeRepository) VerifyChallenge(
	_ context.Context,
	id uuid.UUID,
	phone string,
	purpose Purpose,
	codeHash []byte,
	_ []byte,
	_ time.Time,
	_ time.Time,
) error {
	if id != r.challenge.ID || phone != r.challenge.Phone || purpose != r.challenge.Purpose ||
		!bytes.Equal(codeHash, r.challenge.CodeHash) {
		return ErrInvalidCode
	}
	return nil
}

type fakeSender struct {
	phone string
	code  string
}

func (s *fakeSender) Configured() bool {
	return true
}

func (s *fakeSender) SendCode(_ context.Context, phone, code string) error {
	s.phone = phone
	s.code = code
	return nil
}
