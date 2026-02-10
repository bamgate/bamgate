package config

import (
	"encoding/base64"
	"testing"
)

func TestGeneratePrivateKey(t *testing.T) {
	t.Parallel()

	k, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	if k.IsZero() {
		t.Fatal("generated key is zero")
	}

	// Verify clamping per RFC 7748 §5.
	if k[0]&7 != 0 {
		t.Errorf("key[0] low 3 bits not cleared: 0x%02x", k[0])
	}
	if k[31]&128 != 0 {
		t.Errorf("key[31] high bit not cleared: 0x%02x", k[31])
	}
	if k[31]&64 == 0 {
		t.Errorf("key[31] bit 6 not set: 0x%02x", k[31])
	}
}

func TestGeneratePrivateKey_unique(t *testing.T) {
	t.Parallel()

	k1, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}
	k2, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	if k1 == k2 {
		t.Fatal("two generated keys are identical")
	}
}

func TestPublicKey_deterministic(t *testing.T) {
	t.Parallel()

	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	pub1 := PublicKey(priv)
	pub2 := PublicKey(priv)

	if pub1 != pub2 {
		t.Fatal("PublicKey is not deterministic")
	}

	if pub1.IsZero() {
		t.Fatal("public key is zero")
	}

	if pub1 == priv {
		t.Fatal("public key equals private key")
	}
}

func TestPublicKey_knownVector(t *testing.T) {
	t.Parallel()

	// RFC 7748 §6.1 test vector: Alice's private key → public key.
	// Private key (clamped): 0x77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a
	// Public key: 0x8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a
	privHex := []byte{
		0x77, 0x07, 0x6d, 0x0a, 0x73, 0x18, 0xa5, 0x7d,
		0x3c, 0x16, 0xc1, 0x72, 0x51, 0xb2, 0x66, 0x45,
		0xdf, 0x4c, 0x2f, 0x87, 0xeb, 0xc0, 0x99, 0x2a,
		0xb1, 0x77, 0xfb, 0xa5, 0x1d, 0xb9, 0x2c, 0x2a,
	}
	wantPubHex := []byte{
		0x85, 0x20, 0xf0, 0x09, 0x89, 0x30, 0xa7, 0x54,
		0x74, 0x8b, 0x7d, 0xdc, 0xb4, 0x3e, 0xf7, 0x5a,
		0x0d, 0xbf, 0x3a, 0x0d, 0x26, 0x38, 0x1a, 0xf4,
		0xeb, 0xa4, 0xa9, 0x8e, 0xaa, 0x9b, 0x4e, 0x6a,
	}

	var priv Key
	copy(priv[:], privHex)

	pub := PublicKey(priv)

	var wantPub Key
	copy(wantPub[:], wantPubHex)

	if pub != wantPub {
		t.Errorf("PublicKey mismatch:\n got  %x\n want %x", pub[:], wantPub[:])
	}
}

func TestParseKey_roundTrip(t *testing.T) {
	t.Parallel()

	orig, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	s := orig.String()

	// Verify it's valid base64 of the right length.
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("String() produced invalid base64: %v", err)
	}
	if len(decoded) != KeySize {
		t.Fatalf("String() produced %d decoded bytes, want %d", len(decoded), KeySize)
	}

	parsed, err := ParseKey(s)
	if err != nil {
		t.Fatalf("ParseKey() error: %v", err)
	}

	if parsed != orig {
		t.Errorf("round-trip mismatch:\n orig   %s\n parsed %s", orig, parsed)
	}
}

func TestParseKey_invalidBase64(t *testing.T) {
	t.Parallel()

	_, err := ParseKey("not-valid-base64!!!")
	if err == nil {
		t.Fatal("ParseKey() expected error for invalid base64")
	}
}

func TestParseKey_wrongLength(t *testing.T) {
	t.Parallel()

	// 16 bytes encoded as base64 — wrong length.
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := ParseKey(short)
	if err == nil {
		t.Fatal("ParseKey() expected error for wrong-length key")
	}
}

func TestKey_IsZero(t *testing.T) {
	t.Parallel()

	var zero Key
	if !zero.IsZero() {
		t.Fatal("zero key should report IsZero() == true")
	}

	nonZero, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}
	if nonZero.IsZero() {
		t.Fatal("generated key should report IsZero() == false")
	}
}

func TestKey_MarshalText_roundTrip(t *testing.T) {
	t.Parallel()

	orig, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	text, err := orig.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error: %v", err)
	}

	var decoded Key
	if err := decoded.UnmarshalText(text); err != nil {
		t.Fatalf("UnmarshalText() error: %v", err)
	}

	if decoded != orig {
		t.Errorf("MarshalText/UnmarshalText round-trip mismatch")
	}
}

func TestKey_UnmarshalText_invalid(t *testing.T) {
	t.Parallel()

	var k Key
	if err := k.UnmarshalText([]byte("garbage")); err == nil {
		t.Fatal("UnmarshalText() expected error for invalid input")
	}
}
