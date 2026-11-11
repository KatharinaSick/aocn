// Package session signs and checks who is logged in: a JWT that carries only the GitHub user
// ID, in the website's cookie or as the CLI's token. The GitHub token from logging in is used
// once and never kept.
package session

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Kind is where a token is used. Each kind has its own audience, so a website cookie is no
// CLI token and the other way round.
type Kind struct {
	audience string
	// LastsFor is how long a token of this kind is valid.
	LastsFor time.Duration
}

var (
	// Website is the session cookie. Long enough for the whole of December on one login, short
	// enough that a forgotten browser doesn't stay logged in forever.
	Website = Kind{audience: "website", LastsFor: 30 * 24 * time.Hour}
	// CLI is what aocn login keeps: one login lasts the season, even for a previewer who signs
	// in weeks before it.
	CLI = Kind{audience: "cli", LastsFor: 60 * 24 * time.Hour}
)

// issuer marks tokens this API signed, so a JWT from anywhere else never passes, even one
// signed with the same key by mistake.
const issuer = "aocn.dev"

// ErrInvalid means the token wasn't signed by this API with this key, is for another kind,
// is broken, or expired.
var ErrInvalid = errors.New("invalid session")

// Sign returns a token of the kind for the GitHub user, valid until now + kind.LastsFor.
func Sign(key []byte, githubUserID int64, now time.Time, kind Kind) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   strconv.FormatInt(githubUserID, 10),
		Audience:  jwt.ClaimStrings{kind.audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(kind.LastsFor)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

// Verify returns the GitHub user ID a token of the kind was signed for. Only HS256 is
// accepted, so a token can't pick its own algorithm ("none", or a public key used as an
// HMAC key). Without a key, nothing is valid.
func Verify(key []byte, token string, now time.Time, kind Kind) (int64, error) {
	if len(key) == 0 {
		return 0, ErrInvalid
	}
	claims := jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(kind.audience),
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
