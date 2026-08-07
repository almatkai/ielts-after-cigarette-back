package waitlist

import (
	"context"
	"errors"
	"strings"
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
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)
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
	service := NewService(&fakeRepository{}, &fakeVerifier{claims: validGoogleClaims()}, nil)
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
	service := NewService(&fakeRepository{}, &fakeVerifier{claims: validGoogleClaims()}, nil)
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
	service := NewService(repository, verifier, nil)

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
	service := NewService(repository, &fakeVerifier{err: errors.New("bad token")}, nil)

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
	service := NewService(repository, verifier, nil)

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
			service := NewService(repository, &fakeVerifier{claims: claims}, nil)

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

func TestCheckRequiresGoogleToken(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	_, details, err := service.Check(context.Background(), CheckInput{})
	if err != nil {
		t.Fatal(err)
	}
	if details["googleToken"] != "is required" {
		t.Fatalf("details=%v, want googleToken is required", details)
	}
}

func TestCheckReturnsAccountRegistered(t *testing.T) {
	repository := &fakeRepository{subExists: true}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	result, details, err := service.Check(context.Background(), CheckInput{GoogleToken: " valid-token "})
	if err != nil || len(details) != 0 {
		t.Fatalf("Check() details=%v err=%v", details, err)
	}
	if !result.AccountRegistered {
		t.Fatal("AccountRegistered=false, want true")
	}
	if repository.existsSubQuery != "google-sub-123" {
		t.Fatalf("existsSubQuery=%q, want google-sub-123", repository.existsSubQuery)
	}
}

func TestCheckFlagsTakenPhone(t *testing.T) {
	repository := &fakeRepository{phoneExists: true}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	result, details, err := service.Check(context.Background(), CheckInput{
		GoogleToken: "valid-token",
		Phone:       "+7 (700) 123-45-67",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Check() details=%v err=%v", details, err)
	}
	if !result.PhoneTaken {
		t.Fatal("PhoneTaken=false, want true")
	}
	if repository.existsPhoneQuery != "+77001234567" {
		t.Fatalf("existsPhoneQuery=%q, want +77001234567", repository.existsPhoneQuery)
	}
}

func TestCheckSkipsPhoneLookupWhenAccountRegistered(t *testing.T) {
	repository := &fakeRepository{subExists: true, phoneExists: true}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	result, details, err := service.Check(context.Background(), CheckInput{
		GoogleToken: "valid-token",
		Phone:       "+77001234567",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Check() details=%v err=%v", details, err)
	}
	if result.PhoneTaken {
		t.Fatal("PhoneTaken=true, want false: lookup must be skipped for a registered account")
	}
	if repository.existsPhoneQuery != "" {
		t.Fatalf("existsPhoneQuery=%q, want no phone lookup", repository.existsPhoneQuery)
	}
}

func TestCheckRejectsInvalidPhone(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	_, details, err := service.Check(context.Background(), CheckInput{
		GoogleToken: "valid-token",
		Phone:       "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if details["phone"] == "" {
		t.Fatalf("details=%v, want phone detail", details)
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
	params           CreateParams
	called           bool
	createCalls      int
	createErrs       []error
	subExists        bool
	phoneExists      bool
	existsSubQuery   string
	existsPhoneQuery string
	entries          []Entry
	listErr          error
	admins           map[string]bool
}

func (r *fakeRepository) ExistsByGoogleSub(_ context.Context, googleSub string) (bool, error) {
	r.existsSubQuery = googleSub
	return r.subExists, nil
}

func (r *fakeRepository) List(_ context.Context) ([]Entry, error) {
	return r.entries, r.listErr
}

func (r *fakeRepository) IsAdmin(_ context.Context, email string) (bool, error) {
	return r.admins[email], nil
}

func (r *fakeRepository) ListAdmins(_ context.Context) ([]string, error) {
	emails := make([]string, 0, len(r.admins))
	for email, isAdmin := range r.admins {
		if isAdmin {
			emails = append(emails, email)
		}
	}
	return emails, nil
}

func (r *fakeRepository) AddAdmin(_ context.Context, email string) error {
	if r.admins == nil {
		r.admins = make(map[string]bool)
	}
	r.admins[email] = true
	return nil
}

func (r *fakeRepository) RemoveAdmin(_ context.Context, email string) error {
	delete(r.admins, email)
	return nil
}

func (r *fakeRepository) ExistsByPhone(_ context.Context, phone string) (bool, error) {
	r.existsPhoneQuery = phone
	return r.phoneExists, nil
}

func (r *fakeRepository) Create(_ context.Context, params CreateParams) (Entry, error) {
	r.params = params
	r.called = true
	r.createCalls++
	if r.createCalls <= len(r.createErrs) {
		if err := r.createErrs[r.createCalls-1]; err != nil {
			return Entry{}, err
		}
	}
	entry := Entry{
		ID:           uuid.New(),
		Phone:        params.Phone,
		Status:       "WAITING",
		ReferralCode: params.ReferralCode,
		CreatedAt:    params.CreatedAt,
	}
	if params.Email != "" {
		entry.Email = &params.Email
	}
	if params.GoogleSub != "" {
		entry.GoogleSub = &params.GoogleSub
	}
	if params.ReferredByCode != "" {
		entry.ReferredByCode = &params.ReferredByCode
	}
	return entry, nil
}

func TestListForAdminReturnsEntries(t *testing.T) {
	repository := &fakeRepository{entries: []Entry{{ID: uuid.New(), Phone: "+77001234567", Status: "WAITING"}}}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, []string{"ada@google.com"})

	entries, err := service.ListForAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries)=%d, want 1", len(entries))
	}
}

func TestAddAdminForAdminGrantsAccess(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	if err := service.AddAdminForAdmin(context.Background(), " New@Google.com "); err != nil {
		t.Fatal(err)
	}
	if !repository.admins["new@google.com"] {
		t.Fatalf("admin was not stored: %v", repository.admins)
	}
}

func TestAddAdminForAdminRejectsInvalidEmail(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	if err := service.AddAdminForAdmin(context.Background(), "not-an-email"); !errors.Is(err, ErrAdminEmailInvalid) {
		t.Fatalf("err=%v, want ErrAdminEmailInvalid", err)
	}
}

func TestRemoveAdminForAdminProtectsEnvAdmin(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, []string{"ada@google.com"})

	if err := service.RemoveAdminForAdmin(context.Background(), "ada@google.com"); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("err=%v, want ErrAdminProtected", err)
	}
}

func TestRemoveAdminForAdminDropsDatabaseAdmin(t *testing.T) {
	repository := &fakeRepository{admins: map[string]bool{"old@google.com": true}}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, []string{"ada@google.com"})

	if err := service.RemoveAdminForAdmin(context.Background(), "old@google.com"); err != nil {
		t.Fatal(err)
	}
	if repository.admins["old@google.com"] {
		t.Fatal("admin was not removed")
	}
}

func TestListAdminsForAdminMergesEnvAndDatabase(t *testing.T) {
	repository := &fakeRepository{admins: map[string]bool{"db@google.com": true, "ada@google.com": true}}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, []string{"ada@google.com"})

	admins, err := service.ListAdminsForAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 2 {
		t.Fatalf("len(admins)=%d, want 2: %v", len(admins), admins)
	}
	sources := map[string]string{}
	for _, admin := range admins {
		sources[admin.Email] = admin.Source
	}
	if sources["ada@google.com"] != "env" {
		t.Fatalf("env admin reported as %q", sources["ada@google.com"])
	}
	if sources["db@google.com"] != "db" {
		t.Fatalf("db admin reported as %q", sources["db@google.com"])
	}
}

func TestJoinGeneratesReferralCode(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	entry, details, err := service.Join(context.Background(), JoinInput{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Phone:       "+77001234567",
		GoogleToken: "valid-token",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	code := repository.params.ReferralCode
	if len(code) != referralCodeLength {
		t.Fatalf("ReferralCode=%q, want %d chars", code, referralCodeLength)
	}
	for _, c := range code {
		if !strings.ContainsRune(referralCodeAlphabet, c) {
			t.Fatalf("ReferralCode=%q contains %q outside the alphabet", code, c)
		}
	}
	if entry.ReferralCode != code {
		t.Fatalf("entry.ReferralCode=%q, want stored %q", entry.ReferralCode, code)
	}
}

func TestJoinStoresSanitizedRef(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	_, details, err := service.Join(context.Background(), JoinInput{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Phone:       "+77001234567",
		GoogleToken: "valid-token",
		Ref:         " INSTAGRAM ",
	})
	if err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if repository.params.ReferredByCode != "instagram" {
		t.Fatalf("ReferredByCode=%q, want instagram", repository.params.ReferredByCode)
	}
}

func TestJoinIgnoresInvalidRef(t *testing.T) {
	refs := []string{"bad ref!", "https://spam.example/x", strings.Repeat("a", 65)}
	for _, ref := range refs {
		t.Run(ref, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

			_, details, err := service.Join(context.Background(), JoinInput{
				FirstName:   "Ada",
				LastName:    "Lovelace",
				Phone:       "+77001234567",
				GoogleToken: "valid-token",
				Ref:         ref,
			})
			if err != nil || len(details) != 0 {
				t.Fatalf("Join() details=%v err=%v, want successful join", details, err)
			}
			if repository.params.ReferredByCode != "" {
				t.Fatalf("ReferredByCode=%q, want dropped ref", repository.params.ReferredByCode)
			}
		})
	}
}

func TestJoinRetriesOnReferralCodeCollision(t *testing.T) {
	repository := &fakeRepository{createErrs: []error{ErrReferralCodeCollision, ErrReferralCodeCollision}}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	if _, details, err := service.Join(context.Background(), JoinInput{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Phone:       "+77001234567",
		GoogleToken: "valid-token",
	}); err != nil || len(details) != 0 {
		t.Fatalf("Join() details=%v err=%v", details, err)
	}
	if repository.createCalls != 3 {
		t.Fatalf("createCalls=%d, want 3", repository.createCalls)
	}
}

func TestJoinFailsAfterRepeatedReferralCollisions(t *testing.T) {
	repository := &fakeRepository{createErrs: []error{
		ErrReferralCodeCollision, ErrReferralCodeCollision, ErrReferralCodeCollision,
		ErrReferralCodeCollision, ErrReferralCodeCollision,
	}}
	service := NewService(repository, &fakeVerifier{claims: validGoogleClaims()}, nil)

	_, _, err := service.Join(context.Background(), JoinInput{
		FirstName:   "Ada",
		LastName:    "Lovelace",
		Phone:       "+77001234567",
		GoogleToken: "valid-token",
	})
	if !errors.Is(err, ErrReferralCodeCollision) {
		t.Fatalf("err=%v, want ErrReferralCodeCollision", err)
	}
	if repository.createCalls != maxReferralCodeAttempts {
		t.Fatalf("createCalls=%d, want %d", repository.createCalls, maxReferralCodeAttempts)
	}
}
