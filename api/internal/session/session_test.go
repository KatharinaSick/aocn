package session

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	key = []byte("0123456789abcdef0123456789abcdef")
	now = time.Date(2026, 12, 1, 6, 0, 0, 0, time.UTC)
)

func TestSignedTokenVerifies(t *testing.T) {
	token, err := Sign(key, 9420018, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Verify(key, token, now.Add(LastsFor-time.Second))
	if err != nil || id != 9420018 {
		t.Fatalf("got %d, %v", id, err)
	}
}

func TestExpiredTokenFails(t *testing.T) {
	token, _ := Sign(key, 1, now)
	if _, err := Verify(key, token, now.Add(LastsFor+time.Second)); err != ErrInvalid {
		t.Fatalf("got %v", err)
	}
}

func TestOtherKeyFails(t *testing.T) {
	token, _ := Sign([]byte("another key, another key, another"), 1, now)
	if _, err := Verify(key, token, now); err != ErrInvalid {
		t.Fatalf("got %v", err)
	}
}

func TestForeignTokensFail(t *testing.T) {
	sign := func(method jwt.SigningMethod, signKey any, claims jwt.RegisteredClaims) string {
		s, err := jwt.NewWithClaims(method, claims).SignedString(signKey)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	valid := jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   "1",
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}
	noExpiry := valid
	noExpiry.ExpiresAt = nil
	otherIssuer := valid
	otherIssuer.Issuer = "example.com"
	notAnID := valid
	notAnID.Subject = "octocat"
	zero := valid
	zero.Subject = "0"

	tokens := map[string]string{
		"none":         sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, valid),
		"HS512":        sign(jwt.SigningMethodHS512, key, valid),
		"no expiry":    sign(jwt.SigningMethodHS256, key, noExpiry),
		"other issuer": sign(jwt.SigningMethodHS256, key, otherIssuer),
		"not an ID":    sign(jwt.SigningMethodHS256, key, notAnID),
		"zero":         sign(jwt.SigningMethodHS256, key, zero),
		"garbage":      "not.a.token",
		"empty":        "",
	}
	for name, token := range tokens {
		if _, err := Verify(key, token, now); err != ErrInvalid {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
