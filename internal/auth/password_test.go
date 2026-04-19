package auth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPasswordRules(t *testing.T) {
	cases := map[string]error{
		"":                      ErrPasswordEmpty,
		"short-pass1":           ErrPasswordTooShort, // 11 characters
		"ééééééééééé":           ErrPasswordTooShort, // 11 characters, 22 bytes
		strings.Repeat("a", 73): ErrPasswordTooLong,
		strings.Repeat("é", 37): ErrPasswordTooLong, // 74 bytes
		"correct horse battery": nil,
		strings.Repeat("b", 72): nil,
	}
	for pw, want := range cases {
		err := checkPassword(pw)
		if !errors.Is(err, want) {
			t.Errorf("checkPassword(%d bytes) = %v, want %v", len(pw), err, want)
		}
	}
	if _, err := HashPassword("too-short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("HashPassword accepted a short password: %v", err)
	}
}

func TestHashAndVerify(t *testing.T) {
	const pw = "correct horse battery staple"
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost([]byte(h)); err != nil || cost < BcryptCost {
		t.Errorf("cost = %d, %v", cost, err)
	}
	if strings.Contains(h, pw) {
		t.Error("hash contains the password")
	}
	if !VerifyPassword(h, pw) {
		t.Error("correct password rejected")
	}
	for _, wrong := range []string{"", "correct horse battery stapl", pw + " ", strings.Repeat("x", 100)} {
		if VerifyPassword(h, wrong) {
			t.Errorf("wrong password %q accepted", wrong)
		}
	}
	if VerifyPassword("not-a-hash", pw) {
		t.Error("invalid hash matched")
	}
}

func TestCheckHash(t *testing.T) {
	weak, err := bcrypt.GenerateFromPassword([]byte("whatever-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]string{"empty": "", "garbage": "$2a$xx", "low cost": string(weak)} {
		if err := checkHash(h); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTokens(t *testing.T) {
	a, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newToken()
	if a == b || len(a) != 43 || !wellFormed(a) {
		t.Errorf("tokens %q %q", a, b)
	}
	for _, bad := range []string{"", "abc", a + "x", strings.Repeat("!", 43)} {
		if wellFormed(bad) {
			t.Errorf("wellFormed(%q) = true", bad)
		}
	}
	if len(digest(a)) != 32 {
		t.Error("digest length")
	}
}
