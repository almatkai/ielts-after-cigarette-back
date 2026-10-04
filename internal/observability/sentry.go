package observability

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/getsentry/sentry-go"
)

// SentryErrorSink is a fail-open, non-blocking reporter. It never re-logs an
// already recorded error and never alters the HTTP response. Callers decide
// what counts as a reportable failure; this sink only ships it.
func SentryErrorSink(dsn, environment, release string, logger *slog.Logger) (httpx.ErrorReporter, func(context.Context), error) {
	if dsn == "" {
		return nil, func(context.Context) {}, nil
	}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		Release:          release,
		AttachStacktrace: true,
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			// Breadcrumbs may capture HTTP client calls; drop payloads so a
			// future integration cannot leak student content.
			event.Breadcrumbs = nil
			return event
		},
	})
	if err != nil {
		return nil, func(context.Context) {}, fmt.Errorf("configure Sentry: %w", err)
	}
	hub := sentry.NewHub(client, sentry.NewScope())
	logger.Info("error sink enabled", "kind", "sentry", "environment", environment, "release", release)
	reporter := func(ctx context.Context, event httpx.ErrorEvent) {
		localHub := hub.Clone()
		localHub.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetTag("request_id", event.RequestID)
			scope.SetTag("origin", event.Origin)
			scope.SetTag("path", event.Path)
			if event.ActorID != "" {
				scope.SetUser(sentry.User{ID: event.ActorID})
			}
		})
		switch {
		case event.Panic != nil:
			localHub.RecoverWithContext(ctx, event.Panic)
		case event.Error != nil:
			localHub.CaptureException(event.Error)
		default:
			localHub.CaptureMessage("unknown server error")
		}
	}
	return reporter, func(ctx context.Context) { _ = hub.Client().Flush(2 * time.Second) }, nil
}
