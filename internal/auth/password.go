// Package auth provides operator authentication for "backendlab serve":
// bcrypt password hashing, PostgreSQL-backed sessions with CSRF tokens,
// and login throttling. It is a service layer only; HTTP middleware and
// handlers call it and own cookies and request parsing.
//
// There is a single operator account. Its bcrypt hash is read by the
// caller from the environment variable named by auth.password_hash_env and
// passed to NewService.
package auth

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Password rules.
const (
	// MinPasswordLength is the minimum number of characters.
	MinPasswordLength = 12
	// MaxPasswordBytes is bcrypt's input limit; longer passwords are
	// rejected rather than silently truncated.
	MaxPasswordBytes = 72
	// BcryptCost is the work factor used by HashPassword and the minimum
	// accepted by NewService.
	BcryptCost = 12
)

var (
	// ErrPasswordEmpty is returned for an empty password.
	ErrPasswordEmpty = errors.New("auth: password is empty")
	// ErrPasswordTooShort is returned for passwords under MinPasswordLength characters.
	ErrPasswordTooShort = fmt.Errorf("auth: password must be at least %d characters", MinPasswordLength)
	// ErrPasswordTooLong is returned for passwords over MaxPasswordBytes bytes.
	ErrPasswordTooLong = fmt.Errorf("auth: password must be at most %d bytes", MaxPasswordBytes)
)

func checkPassword(pw string) error {
	switch {
	case pw == "":
		return ErrPasswordEmpty
	case len(pw) > MaxPasswordBytes:
		return ErrPasswordTooLong
	case utf8.RuneCountInString(pw) < MinPasswordLength:
		return ErrPasswordTooShort
	}
	return nil
}

// HashPassword returns the bcrypt hash of pw at BcryptCost after checking
// the length rules.
func HashPassword(pw string) (string, error) {
	if err := checkPassword(pw); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether pw matches the bcrypt hash. The
// comparison inside bcrypt is constant-time. Passwords longer than
// MaxPasswordBytes never match.
func VerifyPassword(hash, pw string) bool {
	if pw == "" || len(pw) > MaxPasswordBytes {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// checkHash validates a configured bcrypt hash and its cost.
func checkHash(hash string) error {
	if hash == "" {
		return errors.New("auth: password hash is empty")
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		return fmt.Errorf("auth: password hash is not a valid bcrypt hash: %w", err)
	}
	if cost < BcryptCost {
		return fmt.Errorf("auth: password hash uses bcrypt cost %d; at least %d is required", cost, BcryptCost)
	}
	return nil
}
