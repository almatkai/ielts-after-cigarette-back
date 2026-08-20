package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/waitlist"
)

const (
	googleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
)

// Google is the OAuth provider for Google sign-in. Exchange trades the
// callback code for an ID token and verifies it with the same verifier as
// the legacy GIS-button flow (internal/waitlist), so both flows resolve to
// the same google identity — the account sub never changes between them.
type Google struct {
	clientID     string
	clientSecret string
	redirectURL  string
	verifier     waitlist.GoogleTokenVerifier
	httpClient   *http.Client
	authorizeURL string
	tokenURL     string
}

func NewGoogle(clientID, clientSecret, redirectURL string, verifier waitlist.GoogleTokenVerifier, httpClient *http.Client) *Google {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Google{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		verifier:     verifier,
		httpClient:   httpClient,
		authorizeURL: googleAuthorizeURL,
		tokenURL:     googleTokenURL,
	}
}

func (p *Google) Name() string { return "google" }

func (p *Google) StartURL(state string) string {
	query := url.Values{
		"response_type": {"code"},
		"client_id":     {p.clientID},
		"redirect_uri":  {p.redirectURL},
		"scope":         {"openid email profile"},
		"state":         {state},
	}
	return p.authorizeURL + "?" + query.Encode()
}

func (p *Google) Exchange(ctx context.Context, code string) (ExternalIdentity, error) {
	idToken, err := p.exchangeCode(ctx, code)
	if err != nil {
		return ExternalIdentity{}, err
	}
	if p.verifier == nil {
		return ExternalIdentity{}, fmt.Errorf("google id token verifier is not configured")
	}
	claims, err := p.verifier.Verify(ctx, idToken)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("verify google id token: %w", err)
	}
	if !claims.EmailVerified || strings.TrimSpace(claims.Email) == "" {
		return ExternalIdentity{}, ErrEmailRequired
	}
	return ExternalIdentity{Sub: claims.Sub, Email: claims.Email, Name: claims.Name}, nil
}

// exchangeCode trades the authorization code at Google's token endpoint; the
// response carries the signed ID token identifying the account.
func (p *Google) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"redirect_uri":  {p.redirectURL},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create google token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", userAgent)

	response, err := p.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("call google token API: %w", err)
	}
	defer response.Body.Close()

	var payload struct {
		IDToken          string `json:"id_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode google token response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if payload.ErrorDescription != "" {
			return "", fmt.Errorf("google token exchange failed: %s", payload.ErrorDescription)
		}
		if payload.Error != "" {
			return "", fmt.Errorf("google token exchange failed: %s", payload.Error)
		}
		return "", fmt.Errorf("google token API returned HTTP %d", response.StatusCode)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("google token exchange failed: %s", payload.Error)
	}
	if payload.IDToken == "" {
		return "", fmt.Errorf("google token response has no id token")
	}
	return payload.IDToken, nil
}
