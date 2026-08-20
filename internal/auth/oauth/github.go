package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubUserURL      = "https://api.github.com/user"
	githubEmailsURL    = "https://api.github.com/user/emails"
)

// GitHub is the OAuth provider for https://github.com/settings/developers
// OAuth apps. The identity sub is the numeric account id, which never changes.
type GitHub struct {
	clientID     string
	clientSecret string
	redirectURL  string
	httpClient   *http.Client
	authorizeURL string
	tokenURL     string
	userURL      string
	emailsURL    string
}

func NewGitHub(clientID, clientSecret, redirectURL string, httpClient *http.Client) *GitHub {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &GitHub{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		httpClient:   httpClient,
		authorizeURL: githubAuthorizeURL,
		tokenURL:     githubTokenURL,
		userURL:      githubUserURL,
		emailsURL:    githubEmailsURL,
	}
}

func (p *GitHub) Name() string { return "github" }

func (p *GitHub) StartURL(state string) string {
	query := url.Values{
		"client_id":    {p.clientID},
		"redirect_uri": {p.redirectURL},
		"scope":        {"read:user user:email"},
		"state":        {state},
	}
	return p.authorizeURL + "?" + query.Encode()
}

func (p *GitHub) Exchange(ctx context.Context, code string) (ExternalIdentity, error) {
	accessToken, err := p.exchangeCode(ctx, code)
	if err != nil {
		return ExternalIdentity{}, err
	}

	user, err := p.fetchUser(ctx, accessToken)
	if err != nil {
		return ExternalIdentity{}, err
	}
	if user.ID == 0 {
		return ExternalIdentity{}, fmt.Errorf("github user response has no id")
	}

	identity := ExternalIdentity{
		Sub:   strconv.FormatInt(user.ID, 10),
		Email: user.Email,
		Name:  user.Name,
	}
	if identity.Name == "" {
		identity.Name = user.Login
	}
	// The public profile email may be empty or unverified; fall back to the
	// account's primary, verified email from the /user/emails endpoint.
	if identity.Email == "" {
		email, err := p.fetchPrimaryEmail(ctx, accessToken)
		if err != nil {
			return ExternalIdentity{}, err
		}
		identity.Email = email
	}
	return identity, nil
}

func (p *GitHub) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"code":          {code},
		"redirect_uri":  {p.redirectURL},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create github token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", userAgent)

	response, err := p.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("call github token API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github token API returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode github token response: %w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("github token exchange failed: %s", payload.Error)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("github token response has no access token")
	}
	return payload.AccessToken, nil
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (p *GitHub) fetchUser(ctx context.Context, accessToken string) (githubUser, error) {
	var user githubUser
	if err := p.getJSON(ctx, p.userURL, accessToken, &user); err != nil {
		return githubUser{}, err
	}
	return user, nil
}

// fetchPrimaryEmail picks the account's primary, verified email — the only
// address trustworthy enough to match or create users by.
func (p *GitHub) fetchPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := p.getJSON(ctx, p.emailsURL, accessToken, &emails); err != nil {
		return "", err
	}
	for _, entry := range emails {
		if entry.Primary && entry.Verified && entry.Email != "" {
			return entry.Email, nil
		}
	}
	return "", ErrEmailRequired
}

func (p *GitHub) getJSON(ctx context.Context, endpoint, accessToken string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create github API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("User-Agent", userAgent)

	response, err := p.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call github API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("github API returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode github API response: %w", err)
	}
	return nil
}
