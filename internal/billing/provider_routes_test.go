package billing

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestSelfAuthenticatingRoute(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/api/v1/billing/callbacks/bcc", true},
		{"POST", "/api/v1/billing/callbacks/fake", true},
		{"GET", "/api/v1/billing/callbacks/bcc", false},
		{"POST", "/api/v1/billing/callbacks/", false},
		{"POST", "/api/v1/billing/callbacks/bcc/x", false},
		{"POST", "/api/v1/billing/bcc/bridge", true},
		{"GET", "/api/v1/billing/bcc/bridge", false},
		{"POST", "/api/v1/billing/bcc/bridge/x", false},
		{"GET", "/api/v1/billing/bcc/return/" + id, true},
		{"POST", "/api/v1/billing/bcc/return/" + id, true},
		{"PUT", "/api/v1/billing/bcc/return/" + id, false},
		{"GET", "/api/v1/billing/bcc/return/", false},
		{"GET", "/api/v1/billing/bcc/return/" + id + "/x", false},
		{"POST", "/billing/callbacks/bcc", false},
		{"POST", "/api/v2/billing/callbacks/bcc", false},
		{"POST", "/api/v1/billing/checkout", false},
	} {
		if got := SelfAuthenticatingRoute(httptest.NewRequest(tc.method, tc.path, nil), "/api/v1"); got != tc.want {
			t.Errorf("%s %s = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestReturnTargetPreservesQuery(t *testing.T) {
	id := uuid.New()
	got, err := returnTarget("https://app.example/app/billing/return?keep=yes", id)
	if err != nil || got != "https://app.example/app/billing/return?keep=yes&orderId="+id.String() {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := returnTarget("://bad", id); err == nil {
		t.Fatal("invalid return URL accepted")
	}
}
