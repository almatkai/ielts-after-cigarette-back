package waitlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const googleTokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"

type GoogleClaims struct {
	Sub           string
	Email         string
	EmailVerified bool
	Name          string
}

type GoogleTokenVerifier interface {
	Verify(ctx context.Context, idToken string) (GoogleClaims, error)
}

type googleTokenVerifier struct {
	clientID   string
	httpClient *http.Client
}

func NewGoogleTokenVerifier(clientID string) GoogleTokenVerifier {
	return &googleTokenVerifier{
		clientID:   clientID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (v *googleTokenVerifier) Verify(ctx context.Context, idToken string) (GoogleClaims, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		googleTokenInfoURL+"?id_token="+url.QueryEscape(idToken),
		nil,
	)
	if err != nil {
		return GoogleClaims{}, fmt.Errorf("create Google tokeninfo request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := v.httpClient.Do(request)
	if err != nil {
		return GoogleClaims{}, fmt.Errorf("call Google tokeninfo API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return GoogleClaims{}, fmt.Errorf("google tokeninfo API returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		Sub           string          `json:"sub"`
		Email         string          `json:"email"`
		EmailVerified json.RawMessage `json:"email_verified"`
		Audience      string          `json:"aud"`
		Name          string          `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return GoogleClaims{}, fmt.Errorf("decode Google tokeninfo response: %w", err)
	}
	if payload.Audience != v.clientID {
		return GoogleClaims{}, fmt.Errorf("google token audience mismatch")
	}
	if payload.Sub == "" {
		return GoogleClaims{}, fmt.Errorf("google token has no sub")
	}

	return GoogleClaims{
		Sub:           payload.Sub,
		Email:         payload.Email,
		EmailVerified: googleEmailVerified(payload.EmailVerified),
		Name:          payload.Name,
	}, nil
}

// tokeninfo returns email_verified as the string "true"/"false", but accept
// a JSON boolean too.
func googleEmailVerified(raw json.RawMessage) bool {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text == "true"
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return false
}
