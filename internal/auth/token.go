package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// tokenBytes is the entropy of session and CSRF tokens.
const tokenBytes = 32

// tokenLen is the length of an encoded token (base64url, no padding).
var tokenLen = base64.RawURLEncoding.EncodedLen(tokenBytes)

// newToken returns 32 random bytes encoded as unpadded base64url.
func newToken() (string, error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// wellFormed reports whether tok has the shape of a token this package
// issues, so obviously invalid values are rejected without a query.
func wellFormed(tok string) bool {
	if len(tok) != tokenLen {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(tok)
	return err == nil
}

// digest returns the SHA-256 of s. Session tokens and throttle keys are
// stored only in this form.
func digest(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// equalTokens compares two tokens in constant time (for equal lengths).
func equalTokens(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
