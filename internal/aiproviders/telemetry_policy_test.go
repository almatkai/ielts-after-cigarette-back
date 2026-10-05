package aiproviders

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

func TestTelemetryOptionalOnlyForExplicitDevelopment(t *testing.T) {
	for _, tc := range []struct {
		environment        string
		optional, required bool
	}{
		{"development", false, true}, {"development", true, false}, {"production", true, true}, {"production", false, true}, {"staging", true, true},
	} {
		service := NewConfigured(nil, config.Config{Environment: tc.environment, AIProviderTelemetryOptional: tc.optional}, testLogger(io.Discard))
		if service.TelemetryRequired() != tc.required {
			t.Fatalf("unsafe telemetry policy: %s optional=%t", tc.environment, tc.optional)
		}
	}
}

func TestLocalAdminCanSaveEnableAndProbeWithoutGlitchTip(t *testing.T) {
	httpx.SetErrorReporter(nil)
	defer httpx.SetErrorReporter(nil)
	service := NewService(&memoryRepo{}, testCipher(t), Provider{}, testLogger(io.Discard))
	service.telemetryOptional = true
	service.publicClient = &http.Client{Transport: probeTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	handler := NewHandler(service, testLogger(io.Discard))
	in := input()
	in.Enabled = true
	body, _ := json.Marshal(in)
	save := httptest.NewRecorder()
	handler.Save(save, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
	if save.Code != 201 {
		t.Fatalf("local save blocked: HTTP %d", save.Code)
	}
	probe := httptest.NewRecorder()
	handler.Test(probe, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
	if probe.Code != 200 || !strings.Contains(probe.Body.String(), `"ok":true`) {
		t.Fatalf("local probe blocked: HTTP %d", probe.Code)
	}
	listing := httptest.NewRecorder()
	handler.List(listing, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(listing.Body.String(), `"errorReportingRequired":false`) {
		t.Fatal("local UI policy missing")
	}
	service.telemetryOptional = false
	for _, operation := range []func(*httptest.ResponseRecorder){
		func(w *httptest.ResponseRecorder) {
			handler.Save(w, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
		},
		func(w *httptest.ResponseRecorder) {
			handler.Test(w, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
		},
	} {
		recorder := httptest.NewRecorder()
		operation(recorder)
		if recorder.Code != 503 || !strings.Contains(recorder.Body.String(), "AI_TELEMETRY_REQUIRED") {
			t.Fatal("default telemetry guard bypassed")
		}
	}
}
