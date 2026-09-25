// Package id generates short, URL-safe, random identifiers.
package id

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// New returns a 128-bit random identifier encoded as 26 lowercase base32
// characters (for example "k3m9q1v8z2x4p0r7t5n6h1c8ba").
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// The system random source failing is not recoverable.
		panic("id: crypto/rand failed: " + err.Error())
	}
	return strings.ToLower(encoding.EncodeToString(b[:]))
}
