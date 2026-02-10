package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// KeySize is the length in bytes of a WireGuard key (Curve25519).
const KeySize = 32

// Key represents a WireGuard key (private or public). It is a 32-byte
// Curve25519 key encoded as base64 in its string representation.
type Key [KeySize]byte

// GeneratePrivateKey generates a new random WireGuard private key.
// The key is clamped per RFC 7748 §5 for use with Curve25519.
func GeneratePrivateKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("generating random key: %w", err)
	}
	clampPrivateKey(&k)
	return k, nil
}

// PublicKey derives the Curve25519 public key from a private key.
func PublicKey(private Key) Key {
	var pub Key
	curve25519.ScalarBaseMult((*[32]byte)(&pub), (*[32]byte)(&private))
	return pub
}

// ParseKey decodes a base64-encoded key string into a Key.
func ParseKey(s string) (Key, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Key{}, fmt.Errorf("decoding base64 key: %w", err)
	}
	if len(b) != KeySize {
		return Key{}, fmt.Errorf("invalid key length: got %d, want %d", len(b), KeySize)
	}
	var k Key
	copy(k[:], b)
	return k, nil
}

// String returns the base64-encoded representation of the key.
func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// IsZero reports whether the key is the zero value (all zeros).
func (k Key) IsZero() bool {
	var zero Key
	return k == zero
}

// MarshalText implements encoding.TextMarshaler for seamless TOML/JSON encoding.
func (k Key) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler for seamless TOML/JSON decoding.
func (k *Key) UnmarshalText(text []byte) error {
	parsed, err := ParseKey(string(text))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// clampPrivateKey applies the Curve25519 clamping from RFC 7748 §5:
//   - Clear the three least significant bits of the first byte
//   - Clear the most significant bit of the last byte
//   - Set the second most significant bit of the last byte
//
// This ensures the private key is a valid Curve25519 scalar.
func clampPrivateKey(k *Key) {
	k[0] &= 248  // Clear bits 0, 1, 2
	k[31] &= 127 // Clear bit 7 (MSB)
	k[31] |= 64  // Set bit 6
}
