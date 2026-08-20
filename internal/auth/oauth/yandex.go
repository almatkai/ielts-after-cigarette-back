package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	yandexAuthorizeURL = "https://oauth.yandex.ru/authorize"
	yandexTokenURL     = "https://oauth.yandex.ru/token"
	yandexInfoURL      = "https://login.yandex.ru/info"
)

// Yandex is the OAuth provider for https://oauth.yandex.ru apps. The identity
// sub is the account id returned as a string by /info.
type Yandex struct {
	clientID     string
	clientSecret string
	redirectURL  string
	httpClient   *http.Client
	authorizeURL string
	tokenURL     string
	infoURL      string
}

func NewYandex(clientID, clientSecret, redirectURL string, httpClient *http.Client) *Yandex {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Yandex{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		httpClient:   httpClient,
		authorizeURL: yandexAuthorizeURL,
		tokenURL:     yandexTokenURL,
		infoURL:      yandexInfoURL,
	}
}

func (p *Yandex) Name() string { return "yandex" }

func (p *Yandex) StartURL(state string) string {
	query := url.Values{
		"response_type": {"code"},
		"client_id":     {p.clientID},
		"redirect_uri":  {p.redirectURL},
		"state":         {state},
	}
	return p.authorizeURL + "?" + query.Encode()
}

func (p *Yandex) Exchange(ctx context.Context, code string) (ExternalIdentity, error) {
	accessToken, err := p.exchangeCode(ctx, code)
	if err != nil {
		return ExternalIdentity{}, err
	}
	return p.fetchIdentity(ctx, accessToken)
}

func (p *Yandex) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create yandex token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", userAgent)

	response, err := p.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("call yandex token API: %w", err)
	}
	defer response.Body.Close()

	var payload struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode yandex token response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if payload.ErrorDescription != "" {
			return "", fmt.Errorf("yandex token exchange failed: %s", payload.ErrorDescription)
		}
		return "", fmt.Errorf("yandex token API returned HTTP %d", response.StatusCode)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("yandex token exchange failed: %s", payload.Error)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("yandex token response has no access token")
	}
	return payload.AccessToken, nil
}

func (p *Yandex) fetchIdentity(ctx context.Context, accessToken string) (ExternalIdentity, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		p.infoURL+"?format=json",
		nil,
	)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("create yandex info request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "OAuth "+accessToken)
	request.Header.Set("User-Agent", userAgent)

	response, err := p.httpClient.Do(request)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("call yandex info API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ExternalIdentity{}, fmt.Errorf("yandex info API returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		ID           string   `json:"id"`
		Login        string   `json:"login"`
		DisplayName  string   `json:"display_name"`
		RealName     string   `json:"real_name"`
		DefaultEmail string   `json:"default_email"`
		Emails       []string `json:"emails"`
		DefaultPhone struct {
			Phone string `json:"phone"`
		} `json:"default_phone"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return ExternalIdentity{}, fmt.Errorf("decode yandex info response: %w", err)
	}
	if payload.ID == "" {
		return ExternalIdentity{}, fmt.Errorf("yandex info response has no id")
	}

	email := payload.DefaultEmail
	if email == "" && len(payload.Emails) > 0 {
		email = payload.Emails[0]
	}
	if email == "" {
		return ExternalIdentity{}, ErrEmailRequired
	}
	name := payload.DisplayName
	if name == "" {
		name = payload.RealName
	}
	if name == "" {
		name = payload.Login
	}
	return ExternalIdentity{Sub: payload.ID, Email: email, Name: name, Phone: payload.DefaultPhone.Phone}, nil
}
