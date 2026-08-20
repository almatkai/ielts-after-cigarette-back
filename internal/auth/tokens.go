package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AccessClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret     []byte
	issuer     string
	audience   string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

func NewTokenManager(secret, issuer, audience string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{
		secret:     []byte(secret),
		issuer:     issuer,
		audience:   audience,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}
}

func (m *TokenManager) NewAccessToken(userID uuid.UUID, role string) (string, time.Time, error) {
	now := m.now().UTC()
	expiresAt := now.Add(m.accessTTL)
	claims := AccessClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{m.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expiresAt, nil
}

func (m *TokenManager) ParseAccessToken(raw string) (AccessClaims, error) {
	var claims AccessClaims
	token, err := jwt.ParseWithClaims(
		raw,
		&claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil || !token.Valid {
		return AccessClaims{}, ErrInvalidCredentials
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return AccessClaims{}, ErrInvalidCredentials
	}
	return claims, nil
}

const (
	// GoogleRegistrationPurpose marks a short-lived token carrying an
	// unregistered social profile between the OAuth sign-in endpoints and
	// /auth/oauth/complete (historically /auth/google/complete). It never
	// authenticates a session.
	GoogleRegistrationPurpose = "google_registration"
	googleRegistrationTTL     = 30 * time.Minute
)

type GoogleRegistrationClaims struct {
	Provider string `json:"provider"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Phone    string `json:"phone,omitempty"`
	Purpose  string `json:"purpose"`
	jwt.RegisteredClaims
}

func (m *TokenManager) NewGoogleRegistrationToken(googleSub, email, name string) (string, error) {
	return m.NewOAuthRegistrationToken("google", googleSub, email, name, "")
}

// NewOAuthRegistrationToken issues a pending-registration token for any OAuth
// provider; the provider claim lets the complete step record the right
// identity row, and the optional phone prefills the registration form.
func (m *TokenManager) NewOAuthRegistrationToken(provider, sub, email, name, phone string) (string, error) {
	now := m.now().UTC()
	claims := GoogleRegistrationClaims{
		Provider: provider,
		Email:    email,
		Name:     name,
		Phone:    phone,
		Purpose:  GoogleRegistrationPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   sub,
			Audience:  jwt.ClaimStrings{m.audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(googleRegistrationTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign oauth registration token: %w", err)
	}
	return signed, nil
}

func (m *TokenManager) ParseGoogleRegistrationToken(raw string) (GoogleRegistrationClaims, error) {
	var claims GoogleRegistrationClaims
	token, err := jwt.ParseWithClaims(
		raw,
		&claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil || !token.Valid {
		return GoogleRegistrationClaims{}, ErrInvalidGoogleToken
	}
	if claims.Purpose != GoogleRegistrationPurpose || claims.Provider == "" || claims.Subject == "" {
		return GoogleRegistrationClaims{}, ErrInvalidGoogleToken
	}
	return claims, nil
}

const (
	// OAuthStatePurpose marks the short-lived token carried as the state
	// parameter between /auth/{provider}/start and /auth/{provider}/callback.
	// It binds the callback to the provider and the frontend path to return
	// to, and never authenticates a session.
	OAuthStatePurpose = "oauth_state"
	oauthStateTTL     = 10 * time.Minute

	// defaultOAuthNext is where the callback redirects when the start request
	// carries no usable next path.
	defaultOAuthNext = "/app/dashboard"
)

type OAuthStateClaims struct {
	Provider string `json:"provider"`
	Next     string `json:"next"`
	Purpose  string `json:"purpose"`
	jwt.RegisteredClaims
}

func (m *TokenManager) NewOAuthStateToken(provider, next string) (string, error) {
	now := m.now().UTC()
	claims := OAuthStateClaims{
		Provider: provider,
		Next:     sanitizeOAuthNext(next),
		Purpose:  OAuthStatePurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   provider,
			Audience:  jwt.ClaimStrings{m.audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(oauthStateTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign oauth state token: %w", err)
	}
	return signed, nil
}

func (m *TokenManager) ParseOAuthStateToken(raw string) (OAuthStateClaims, error) {
	var claims OAuthStateClaims
	token, err := jwt.ParseWithClaims(
		raw,
		&claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil || !token.Valid {
		return OAuthStateClaims{}, ErrInvalidOAuthState
	}
	if claims.Purpose != OAuthStatePurpose || claims.Provider == "" {
		return OAuthStateClaims{}, ErrInvalidOAuthState
	}
	return claims, nil
}

// sanitizeOAuthNext restricts the post-login redirect to frontend-relative
// paths under /app/ so a crafted start URL cannot bounce the browser to an
// arbitrary origin; anything else falls back to the dashboard.
func sanitizeOAuthNext(next string) string {
	next = strings.TrimSpace(next)
	if !strings.HasPrefix(next, "/app/") {
		return defaultOAuthNext
	}
	return next
}

func (m *TokenManager) NewRefreshToken() (raw string, hash []byte, expiresAt time.Time, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", nil, time.Time{}, fmt.Errorf("generate refresh token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(bytes)
	hash = HashRefreshToken(raw)
	expiresAt = m.now().UTC().Add(m.refreshTTL)
	return raw, hash, expiresAt, nil
}

func HashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func (m *TokenManager) AccessTTL() time.Duration {
	return m.accessTTL
}
