// Package turn provides TURN credential generation and a WebSocket proxy dialer
// for routing TURN traffic through a Cloudflare Worker.
package turn

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultCredentialLifetime is the default validity period for TURN credentials.
	DefaultCredentialLifetime = 24 * time.Hour

	// DefaultRealm is the realm used in the long-term credential mechanism.
	DefaultRealm = "bamgate"
)

// GenerateCredentials creates time-limited TURN REST API credentials from a shared secret.
// The username encodes the expiry timestamp and peer ID. The password is an HMAC-SHA1
// of the username, keyed by the shared secret.
//
// This follows the TURN REST API convention used by coturn and supported by pion/ice:
//
//	username = "<unix_expiry>:<peerID>"
//	password = base64(HMAC-SHA1(secret, username))
func GenerateCredentials(secret, peerID string, lifetime time.Duration) (username, password string) {
	if lifetime == 0 {
		lifetime = DefaultCredentialLifetime
	}
	expiry := time.Now().Add(lifetime).Unix()
	username = fmt.Sprintf("%d:%s", expiry, peerID)
	password = computePassword(secret, username)
	return username, password
}

// ValidateCredentials checks that TURN REST API credentials are valid and not expired.
// It recomputes the password from the shared secret and compares it to the provided password.
func ValidateCredentials(secret, username, password string) error {
	// Parse expiry from username.
	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid username format: expected '<expiry>:<peerID>'")
	}

	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid expiry in username: %w", err)
	}

	if time.Now().Unix() > expiry {
		return fmt.Errorf("credentials expired at %d", expiry)
	}

	expected := computePassword(secret, username)
	if !hmac.Equal([]byte(password), []byte(expected)) {
		return fmt.Errorf("invalid password")
	}

	return nil
}

// DeriveAuthKey computes the long-term credential key used for STUN MESSAGE-INTEGRITY:
//
//	key = MD5(username + ":" + realm + ":" + password)
//
// This is per RFC 5389 Section 15.4.
func DeriveAuthKey(username, realm, password string) []byte {
	h := md5.New() //nolint:gosec // MD5 is required by the STUN/TURN spec.
	h.Write([]byte(username + ":" + realm + ":" + password))
	return h.Sum(nil)
}

// computePassword generates the HMAC-SHA1 password for TURN REST API credentials.
func computePassword(secret, username string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
