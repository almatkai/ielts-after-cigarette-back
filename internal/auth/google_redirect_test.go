package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func googleFormRequest(credential, csrf string) *http.Request {
	form := url.Values{"credential": {credential}, "g_csrf_token": {csrf}}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "g_csrf_token", Value: "csrf-test"})
	return r
}

func TestGoogleFormReturnsToAppWithoutPopup(t *testing.T) {
	for _, credential := range []string{"admin", "person"} {
		t.Run(credential, func(t *testing.T) {
			service, _ := testGoogleService("admin@example.com")
			response := httptest.NewRecorder()
			testAuthHandler(service).GoogleLogin(response, googleFormRequest(credential, "csrf-test"))
			if response.Code != http.StatusSeeOther {
				t.Fatalf("Google return must navigate back to app, got %d: %s", response.Code, response.Body.String())
			}
			location := response.Header().Get("Location")
			if !strings.HasPrefix(location, "/app/login?google=") {
				t.Fatalf("expected app return, got %q", location)
			}
			if strings.Contains(location, "token") || strings.Contains(location, credential) {
				t.Fatalf("credentials must stay out of redirect URL: %q", location)
			}
			cookies := response.Result().Cookies()
			if len(cookies) == 0 {
				t.Fatal("Google return must persist session or pending registration")
			}
			for _, cookie := range cookies {
				if !cookie.HttpOnly || cookie.Path != "/api/v1/auth" {
					t.Fatalf("unexpected cookie attributes: %+v", cookie)
				}
			}
		})
	}
}

func TestGoogleFormRejectsInvalidCSRFAndCredential(t *testing.T) {
	for _, test := range []struct{ credential, csrf string }{
		{"admin", ""}, {"admin", "wrong"}, {"bogus", "csrf-test"}, {"", "csrf-test"},
	} {
		service, repository := testGoogleService("admin@example.com")
		response := httptest.NewRecorder()
		testAuthHandler(service).GoogleLogin(response, googleFormRequest(test.credential, test.csrf))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/app/login?google=error" {
			t.Fatalf("failed Google return must show a retry, got %d %q", response.Code, response.Header().Get("Location"))
		}
		if len(repository.sessions) != 0 {
			t.Fatal("invalid Google return issued a session")
		}
	}
	service, _ := testGoogleService("admin@example.com")
	request := googleFormRequest("admin", "csrf-test")
	request.Header.Del("Cookie")
	response := httptest.NewRecorder()
	testAuthHandler(service).GoogleLogin(response, request)
	if response.Header().Get("Location") != "/app/login?google=error" {
		t.Fatal("missing CSRF cookie must reject sign-in")
	}
}

func TestGoogleRedirectRestoresAndCompletesRegistration(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	repository.seedLead("person@example.com", "+77001234567", "Waitlist", "Person", "")
	handler := testAuthHandler(service)
	response := httptest.NewRecorder()
	handler.GoogleLogin(response, googleFormRequest("person", "csrf-test"))
	var pendingCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "ielts_refresh_google_registration" {
			pendingCookie = cookie
		}
	}
	if pendingCookie == nil || pendingCookie.Value == "" {
		t.Fatal("pending registration cookie is missing")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/pending", nil)
	request.AddCookie(pendingCookie)
	response = httptest.NewRecorder()
	handler.PendingGoogleRegistration(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("profile restore failed: %d %s", response.Code, response.Body.String())
	}
	var pending pendingRegistrationResponse
	decodeBody(t, response, &pending)
	if pending.Profile.Email != "person@example.com" || pending.Profile.Name != "Waitlist Person" || pending.Profile.Phone != "+77001234567" {
		t.Fatalf("waitlist profile was not preserved: %+v", pending.Profile)
	}
	body := `{"registrationToken":"` + pending.RegistrationToken + `","name":"Person Personov","phone":"+77001234567","acceptedTerms":true}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/google/complete", strings.NewReader(body))
	request.AddCookie(pendingCookie)
	response = httptest.NewRecorder()
	handler.CompleteGoogleRegistration(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("registration failed: %d %s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == pendingCookie.Name && cookie.MaxAge == -1 {
			return
		}
	}
	t.Fatal("completed registration must clear the pending cookie")
}

func TestGoogleRedirectRejectsMissingOrInvalidPendingCookie(t *testing.T) {
	for _, token := range []string{"", "invalid"} {
		service, _ := testGoogleService("admin@example.com")
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/pending", nil)
		if token != "" {
			request.AddCookie(&http.Cookie{Name: "ielts_refresh_google_registration", Value: token})
		}
		response := httptest.NewRecorder()
		testAuthHandler(service).PendingGoogleRegistration(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid pending cookie accepted: %d", response.Code)
		}
	}
}

func TestGoogleRedirectPreservesLeadMatchedByGoogleSub(t *testing.T) {
	service, repository := testGoogleService("admin@example.com")
	repository.seedLead("placeholder@example.com", "+77001234567", "Waitlist", "Person", "sub-person")
	handler := testAuthHandler(service)
	response := httptest.NewRecorder()
	handler.GoogleLogin(response, googleFormRequest("person", "csrf-test"))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/pending", nil)
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	response = httptest.NewRecorder()
	handler.PendingGoogleRegistration(response, request)
	var pending pendingRegistrationResponse
	decodeBody(t, response, &pending)
	if response.Code != http.StatusOK || pending.Profile.Email != "person@example.com" || pending.Profile.Name != "Waitlist Person" || pending.Profile.Phone != "+77001234567" {
		t.Fatalf("Google subject match lost the lead profile: %d %+v", response.Code, pending.Profile)
	}
}
