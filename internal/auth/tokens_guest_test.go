package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestGuestCannotReceiveOrUseAccountTokens(t *testing.T) {
	manager := NewTokenManager("test-secret", "issuer", "audience", time.Hour, time.Hour)
	if _, _, err := manager.NewAccessToken(uuid.New(), "GUEST"); err == nil {
		t.Fatal("issued an account token to a guest")
	}
	claims := AccessClaims{Role: "GUEST", RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString(), Issuer: "issuer", Audience: jwt.ClaimStrings{"audience"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(manager.secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ParseAccessToken(token); err == nil {
		t.Fatal("accepted a signed guest token at account boundary")
	}
}
