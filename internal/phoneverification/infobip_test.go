package phoneverification

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInfobipSenderUsesAuthenticationTemplate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/whatsapp/1/message/template" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "App secret-api-key" {
			t.Errorf("Authorization=%q", got)
		}
		var payload infobipTemplateRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) != 1 {
			t.Fatalf("messages=%d, want 1", len(payload.Messages))
		}
		message := payload.Messages[0]
		if message.From != "447860099299" || message.To != "77001234567" {
			t.Errorf("unexpected sender or recipient: %+v", message)
		}
		if message.Content.TemplateName != "phone_verification" || message.Content.Language != "en" {
			t.Errorf("unexpected template content: %+v", message.Content)
		}
		if got := message.Content.TemplateData.Body.Placeholders[0]; got != "123456" {
			t.Errorf("body code=%q", got)
		}
		if got := message.Content.TemplateData.Buttons[0].Parameter; got != "123456" {
			t.Errorf("button code=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"name":"PENDING_ENROUTE"}}`))
	}))
	t.Cleanup(server.Close)

	sender := NewInfobipSender(
		server.URL,
		"secret-api-key",
		"447860099299",
		"phone_verification",
		"en",
		&http.Client{Timeout: time.Second},
	)
	if err := sender.SendCode(t.Context(), "+77001234567", "123456"); err != nil {
		t.Fatalf("SendCode() error=%v", err)
	}
}

func TestInfobipSenderRequiresConfiguration(t *testing.T) {
	sender := NewInfobipSender(
		"https://example.com", "", "", "", "en", &http.Client{Timeout: time.Second},
	)
	if err := sender.SendCode(t.Context(), "+77001234567", "123456"); err != ErrSenderNotConfigured {
		t.Fatalf("SendCode() error=%v, want ErrSenderNotConfigured", err)
	}
}
