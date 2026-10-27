package cfhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/bridge"
	"github.com/google/uuid"
	"io"
	"net/http"
)

// AudioData is an internal wire descriptor. Only the native fetch adapter sees
// it; the actual AI endpoint receives the original base64 JSON string.
func AudioData(key, encoded string) any {
	if key != "" {
		return map[string]string{"$cfAudio": key}
	}
	return encoded
}

type Transport struct{ Protected bool }

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	id := uuid.NewString()
	done := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			_ = bridge.Call(context.WithoutCancel(r.Context()), "fetch.close", id, nil)
		case <-done:
		}
	}()
	var out struct {
		ID      string
		Status  int
		Headers map[string][]string
		Length  int64
	}
	err := bridge.Bytes(r.Context(), "fetch.start", map[string]any{"id": id, "url": r.URL.String(), "method": r.Method, "headers": r.Header, "protected": t.Protected}, body, &out)
	if err != nil {
		close(done)
		return nil, err
	}
	headers := make(http.Header)
	for key, values := range out.Headers {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	return &http.Response{StatusCode: out.Status, Header: headers, ContentLength: out.Length, Body: &fetchReader{ctx: r.Context(), id: out.ID, done: done}, Request: r}, nil
}

type fetchReader struct {
	ctx    context.Context
	id     string
	closed bool
	done   chan struct{}
}

func (r *fetchReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.closed {
		return 0, io.EOF
	}
	var value *string
	if err := bridge.Call(r.ctx, "fetch.read", map[string]any{"id": r.id, "length": min(len(p), 64<<10)}, &value); err != nil {
		return 0, err
	}
	if value == nil {
		return 0, io.EOF
	}
	bytes, err := base64.StdEncoding.DecodeString(*value)
	if err != nil {
		return 0, err
	}
	return copy(p, bytes), nil
}
func (r *fetchReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	close(r.done)
	return bridge.Call(context.WithoutCancel(r.ctx), "fetch.close", r.id, nil)
}

func TranscriptionRequest(ctx context.Context, url, filename, key string) (*http.Request, error) {
	body, err := json.Marshal(map[string]string{"key": key, "filename": filename})
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/x-iac-native-transcription")
	return r, nil
}
