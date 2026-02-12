package turn

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateCredentials(t *testing.T) {
	t.Parallel()

	secret := "test-secret-key"
	peerID := "home-server"

	username, password := GenerateCredentials(secret, peerID, DefaultCredentialLifetime)

	// Username should be "<expiry>:<peerID>".
	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("username format: got %q, want '<expiry>:<peerID>'", username)
	}
	if parts[1] != peerID {
		t.Errorf("peer ID: got %q, want %q", parts[1], peerID)
	}

	// Password should be non-empty base64.
	if password == "" {
		t.Fatal("password is empty")
	}
}

func TestGenerateCredentials_DefaultLifetime(t *testing.T) {
	t.Parallel()

	username, _ := GenerateCredentials("secret", "peer", 0)

	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("username format: got %q", username)
	}
	// With default lifetime (24h), expiry should be ~24h from now.
	// Allow 5 seconds of slack.
	expected := time.Now().Add(DefaultCredentialLifetime).Unix()
	got := mustParseInt(t, parts[0])
	if abs(got-expected) > 5 {
		t.Errorf("expiry: got %d, want ~%d (within 5s)", got, expected)
	}
}

func TestValidateCredentials_Valid(t *testing.T) {
	t.Parallel()

	secret := "shared-secret"
	username, password := GenerateCredentials(secret, "laptop", DefaultCredentialLifetime)

	if err := ValidateCredentials(secret, username, password); err != nil {
		t.Fatalf("valid credentials rejected: %v", err)
	}
}

func TestValidateCredentials_Expired(t *testing.T) {
	t.Parallel()

	secret := "shared-secret"
	// Craft credentials with an expiry in the past (1 second ago).
	username := "1:laptop" // Unix timestamp 1 is far in the past.
	password := computePassword(secret, username)

	err := ValidateCredentials(secret, username, password)
	if err == nil {
		t.Fatal("expired credentials accepted")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention 'expired': %v", err)
	}
}

func TestValidateCredentials_WrongSecret(t *testing.T) {
	t.Parallel()

	username, password := GenerateCredentials("secret-A", "peer", DefaultCredentialLifetime)

	err := ValidateCredentials("secret-B", username, password)
	if err == nil {
		t.Fatal("wrong secret accepted")
	}
	if !strings.Contains(err.Error(), "invalid password") {
		t.Errorf("error should mention 'invalid password': %v", err)
	}
}

func TestValidateCredentials_MalformedUsername(t *testing.T) {
	t.Parallel()

	err := ValidateCredentials("secret", "no-colon-here", "password")
	if err == nil {
		t.Fatal("malformed username accepted")
	}
	if !strings.Contains(err.Error(), "invalid username format") {
		t.Errorf("error should mention 'invalid username format': %v", err)
	}
}

func TestValidateCredentials_BadExpiry(t *testing.T) {
	t.Parallel()

	err := ValidateCredentials("secret", "notanumber:peer", "password")
	if err == nil {
		t.Fatal("bad expiry accepted")
	}
	if !strings.Contains(err.Error(), "invalid expiry") {
		t.Errorf("error should mention 'invalid expiry': %v", err)
	}
}

func TestDeriveAuthKey(t *testing.T) {
	t.Parallel()

	key := DeriveAuthKey("user", "realm", "pass")
	if len(key) != 16 { // MD5 output is 16 bytes
		t.Fatalf("auth key length: got %d, want 16", len(key))
	}

	// Same inputs should produce same key.
	key2 := DeriveAuthKey("user", "realm", "pass")
	if string(key) != string(key2) {
		t.Error("same inputs produced different keys")
	}

	// Different inputs should produce different key.
	key3 := DeriveAuthKey("user", "realm", "different")
	if string(key) == string(key3) {
		t.Error("different inputs produced same key")
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	t.Parallel()

	// Generate credentials, derive auth key on both sides, verify they match.
	secret := "my-turn-secret"
	peerID := "phone"

	username, password := GenerateCredentials(secret, peerID, DefaultCredentialLifetime)

	// Client derives auth key using username, realm, and password.
	clientKey := DeriveAuthKey(username, DefaultRealm, password)

	// Server validates credentials and derives the same auth key.
	if err := ValidateCredentials(secret, username, password); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	serverKey := DeriveAuthKey(username, DefaultRealm, password)

	if string(clientKey) != string(serverKey) {
		t.Error("client and server derived different auth keys")
	}
}

func mustParseInt(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
