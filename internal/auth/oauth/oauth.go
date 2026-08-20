// Package oauth implements the redirect-based OAuth 2.0 providers (Google,
// GitHub, Yandex) for social sign-in. Google also keeps a legacy ID-token
// endpoint from the old GIS-button flow (internal/waitlist); the HTTP calls
// here are hand-rolled on purpose, matching that verifier — no oauth2 library.
package oauth

import (
	"context"
	"errors"
)

// ErrEmailRequired is returned when the provider account does not expose a
// verified email address, which the platform requires for sign-in.
var ErrEmailRequired = errors.New("oauth account has no verified email")

// ExternalIdentity is a verified OAuth profile, normalized across providers.
type ExternalIdentity struct {
	Sub   string
	Email string
	Name  string
	// Phone is optional: Yandex exposes a default phone number, GitHub none.
	Phone string
}

// Provider is one OAuth 2.0 authorization-code provider. StartURL builds the
// authorize redirect carrying the state token; Exchange trades the callback
// code for an access token and reads the normalized profile.
type Provider interface {
	Name() string
	StartURL(state string) string
	Exchange(ctx context.Context, code string) (ExternalIdentity, error)
}

// userAgent identifies the backend on provider API calls; GitHub rejects
// requests without a User-Agent.
const userAgent = "ielts-after-cigarette-back"
