// Package auth provides the credential primitives behind the usecase ports:
// bcrypt password hashing and opaque session tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

type Bcrypt struct{ cost int }

// NewBcrypt uses the default cost when cost is 0 (tests pass bcrypt.MinCost).
func NewBcrypt(cost int) Bcrypt {
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	return Bcrypt{cost}
}

func (b Bcrypt) Hash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), b.cost)
	return string(h), err
}

func (b Bcrypt) Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Tokens issues 256-bit random tokens. The server keeps only the SHA-256
// digest, so a leaked database cannot be replayed as valid sessions.
type Tokens struct{}

func (Tokens) New() (token, digest string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, Tokens{}.Digest(token), nil
}

func (Tokens) Digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
