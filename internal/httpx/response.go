package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ClientClosedRequest is the non-standard status nginx uses for a request whose
// client disconnected before the response was written. Answering 499 instead of
// 500 keeps routine cancellations (a navigation away, a reload, a hot reload,
// or the double mount that React StrictMode performs in development) out of the
// server error rate, where they used to show up as failures caused by
// "context canceled".
const ClientClosedRequest = 499

type ErrorResponse struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
}

// Canceled reports whether the error means the client went away before the
// response was written. A server-side timeout is deliberately not included:
// that case still has a waiting client and is answered by the timeout
// middleware.
func Canceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

// ClientGone answers 499 and reports true when the error is a client
// disconnection. Handlers call it first in their unexpected-error branch so a
// cancelled request is not logged as a failure.
func ClientGone(w http.ResponseWriter, r *http.Request, err error) bool {
	if !Canceled(err) {
		return false
	}
	w.WriteHeader(ClientClosedRequest)
	return true
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]string) {
	WriteJSON(w, status, ErrorResponse{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: RequestID(r.Context()),
	})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain a single JSON object")
		}
		return err
	}
	return nil
}
