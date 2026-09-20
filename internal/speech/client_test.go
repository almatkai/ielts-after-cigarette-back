package speech

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientTranscribe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing service token")
		}
		file, _, err := r.FormFile("audio")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if string(data) != "audio" {
			t.Fatalf("unexpected audio: %q", data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"hello","language":"en","durationSeconds":1,"durationAfterVadSeconds":0.8,"words":[{"word":"hello","start":0.1,"end":0.5,"probability":0.99}]}`)
	}))
	defer server.Close()

	result, err := NewClient(server.URL, "secret", server.Client()).Transcribe(context.Background(), "answer.webm", "audio/webm", strings.NewReader("audio"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" || len(result.Words) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
