// Package session signs and checks who is logged in on the website: a JWT in a cookie that
// carries only the GitHub user ID. The GitHub token from the login is never kept.
package session

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// LastsFor is how long a login on the website lasts. Long enough for the whole of December
// on one login, short enough that a forgotten browser doesn't stay logged in forever.
const LastsFor = 30 * 24 * time.Hour

// issuer marks tokens this API signed, so a JWT from anywhere else never passes, even one
// signed with the same key by mistake.
const issuer = "aocn.dev"

// ErrInvalid means the token wasn't signed by this API with this key, is broken, or expired.
var ErrInvalid = errors.New("invalid session")

// Sign returns a token for the GitHub user that's valid until now + LastsFor.
func Sign(key []byte, githubUserID int64, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   strconv.FormatInt(githubUserID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(LastsFor)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

// Verify returns the GitHub user ID a token was signed for. Only HS256 is accepted, so a
// token can't pick its own algorithm ("none", or a public key used as an HMAC key).
func Verify(key []byte, token string, now time.Time) (int64, error) {
	claims := jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return 0, ErrInvalid
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalid
	}
	return id, nil
}
