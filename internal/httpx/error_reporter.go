package httpx

import (
	"context"

	"github.com/google/uuid"
)

// ErrorEvent carries the minimum needed to correlate a server error with the
// access log. No request bodies, answers, tokens or URLs are ever included.
type ErrorEvent struct {
	Error       error
	RequestID   string
	ActorID     string
	Method      string
	Path        string
	Panic       any
	AttemptID   uuid.UUID
	RecordingID uuid.UUID
	// Origin names the reporting site: "http" for handlers, plus a specific
	// background pipeline name from the workers.
	Origin     string
	ProviderID uuid.UUID
	AIPurpose  string
}

// ErrorReporter is a non-blocking sink for unexpected server errors. A future
// tracker can be wired here without coupling handlers to its SDK.
type ErrorReporter func(context.Context, ErrorEvent)

var errorReporter ErrorReporter

// SetErrorReporter installs the process-wide sink. It is safe to call once
// during startup before serving; later calls replace the sink.
func SetErrorReporter(reporter ErrorReporter) { errorReporter = reporter }

// ErrorReporterInstalled reports whether a sink has been configured.
func ErrorReporterInstalled() bool { return errorReporter != nil }

func reportError(ctx context.Context, event ErrorEvent) {
	if errorReporter == nil {
		return
	}
	// A broken sink must not change the HTTP response or panic.
	defer func() { _ = recover() }()
	errorReporter(ctx, event)
}

// ReportBackground reports an unexpected background pipeline failure.
// origin identifies the reporting component. The following variadic key/value
// pairs are restricted to a fixed allow-list; unsupported keys are ignored so
// that a mistake cannot leak unexpected values into an external tracker.
func ReportBackground(ctx context.Context, origin string, fields ...any) {
	event := ErrorEvent{Origin: origin}
	for i := 0; i+1 < len(fields); i += 2 {
		key, _ := fields[i].(string)
		switch key {
		case "request_id":
			event.RequestID, _ = fields[i+1].(string)
		case "user_id":
			event.ActorID, _ = fields[i+1].(string)
		case "error":
			if err, ok := fields[i+1].(error); ok {
				event.Error = err
			}
		case "attempt_id":
			if id, ok := fields[i+1].(uuid.UUID); ok {
				event.AttemptID = id
			}
		case "provider_id":
			if id, ok := fields[i+1].(uuid.UUID); ok {
				event.ProviderID = id
			}
		case "ai_purpose":
			event.AIPurpose, _ = fields[i+1].(string)
		case "recording_id":
			if id, ok := fields[i+1].(uuid.UUID); ok {
				event.RecordingID = id
			}
		}
	}
	reportError(ctx, event)
}
