package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lassegit/neolib/internal/auth"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const password = "correct horse battery staple"

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash %q is not argon2id", hash)
	}

	ok, err := auth.VerifyPassword(hash, password)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("correct password was rejected")
	}

	ok, err = auth.VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Fatal("wrong password was accepted")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, encoded := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536$c2FsdA$aGFzaA",
	} {
		if _, err := auth.VerifyPassword(encoded, "x"); !errors.Is(err, auth.ErrInvalidHash) {
			t.Errorf("VerifyPassword(%q) error = %v, want ErrInvalidHash", encoded, err)
		}
	}
}
