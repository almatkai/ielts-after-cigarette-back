package aiproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type probeTransport func(*http.Request) (*http.Response, error)

func (f probeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeUsesEncryptedKeyWithoutReturningOrChangingIt(t *testing.T) {
	repo := &memoryRepo{}
	service := NewService(repo, testCipher(t), Provider{}, testLogger(io.Discard))
	saved, err := service.Save(context.Background(), uuid.Nil, uuid.New(), input())
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte{}, saved.Ciphertext...)
	calls := 0
	status := 200
	var host string
	service.publicClient = &http.Client{Transport: probeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		host = r.URL.Host
		if r.Header.Get("Authorization") != "Bearer secret-provider-key" {
			t.Error("probe did not decrypt correct key")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "secret-provider-key") {
			t.Error("key in request body")
		}
		responseBody := `{"choices":[{"message":{"content":"OK"}}]}`
		if status != 200 {
			responseBody = `secret-provider-key raw-provider-error`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody)), Request: r}, nil
	})}
	result, err := service.Test(context.Background(), saved.ID, nil)
	if err != nil || !result.OK || calls != 1 {
		t.Fatalf("probe failed: %+v %v", result, err)
	}
	changed := input()
	changed.APIKey = ""
	changed.Revision = saved.Revision
	changed.Endpoint = "https://edited.example.com/v1/chat/completions"
	if result, err := service.Test(context.Background(), saved.ID, &changed); err != nil || !result.OK || host != "edited.example.com" {
		t.Fatal("unsaved settings not tested with stored secret")
	}
	if !bytes.Equal(repo.items[0].Ciphertext, before) || repo.items[0].Endpoint != saved.Endpoint {
		t.Fatal("probe modified configuration")
	}
	sink := &captureSink{}
	httpx.SetErrorReporter(sink.ReportError)
	defer httpx.SetErrorReporter(nil)
	status = 401
	result, err = service.Test(context.Background(), saved.ID, nil)
	data, _ := json.Marshal(result)
	if err != nil || result.OK || result.Code != "http_error" || result.HTTPStatus != 401 || strings.Contains(string(data), "secret") || len(sink.events) != 1 {
		t.Fatalf("unsafe or unreported failed probe: %+v %v", result, err)
	}
}
