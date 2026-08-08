package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
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
	// unregistered Google profile between /auth/google and
	// /auth/google/complete. It never authenticates a session.
	GoogleRegistrationPurpose = "google_registration"
	googleRegistrationTTL     = 30 * time.Minute
)

type GoogleRegistrationClaims struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	jwt.RegisteredClaims
}

func (m *TokenManager) NewGoogleRegistrationToken(googleSub, email, name string) (string, error) {
	now := m.now().UTC()
	claims := GoogleRegistrationClaims{
		Email:   email,
		Name:    name,
		Purpose: GoogleRegistrationPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   googleSub,
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
		return "", fmt.Errorf("sign google registration token: %w", err)
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
	if claims.Purpose != GoogleRegistrationPurpose || claims.Subject == "" {
		return GoogleRegistrationClaims{}, ErrInvalidGoogleToken
	}
	return claims, nil
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
