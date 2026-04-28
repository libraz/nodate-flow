package token

import (
	"strings"
	"testing"
)

// TestMintTokenShape asserts that MintToken returns a non-empty
// base64url-ish raw token and a 64-character lowercase hex hash. The
// "ish" qualifier matters: base64url uses `-` and `_` so we cannot
// blanket-reject those, but the token must not contain padding (`=`)
// or the standard base64 sigils (`+` / `/`).
func TestMintTokenShape(t *testing.T) {
	raw, hash, err := MintToken()
	if err != nil {
		t.Fatalf("MintToken returned err: %v", err)
	}
	if raw == "" {
		t.Fatal("MintToken returned empty raw token")
	}
	if strings.ContainsAny(raw, "=+/") {
		t.Fatalf("raw token %q must use base64url without padding", raw)
	}
	if len(hash) != 64 {
		t.Fatalf("hash length = %d; want 64 (hex SHA-256)", len(hash))
	}
	for _, c := range hash {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			t.Fatalf("hash %q contains non-hex char %q", hash, c)
		}
	}
}

// TestMintTokenUnique asserts two mints produce different raw tokens.
// This is a property of crypto/rand and not strictly a code-under-test
// guarantee, but a guard against regressing to a deterministic source.
func TestMintTokenUnique(t *testing.T) {
	raw1, _, err := MintToken()
	if err != nil {
		t.Fatalf("MintToken err: %v", err)
	}
	raw2, _, err := MintToken()
	if err != nil {
		t.Fatalf("MintToken err: %v", err)
	}
	if raw1 == raw2 {
		t.Fatalf("two mints produced identical tokens %q — randomness is broken", raw1)
	}
}

// TestHashTokenStable asserts HashToken is deterministic, so a
// re-presented token always hashes to the same column value the row
// was created with.
func TestHashTokenStable(t *testing.T) {
	const sample = "abcdef123456"
	if got1, got2 := HashToken(sample), HashToken(sample); got1 != got2 {
		t.Fatalf("HashToken non-deterministic: %q vs %q", got1, got2)
	}
}

// TestHashTokenMatchesMint asserts that hashing the freshly minted raw
// token reproduces the mint's own hash output. Round-trip protects
// against a future refactor that would split the encoding paths.
func TestHashTokenMatchesMint(t *testing.T) {
	raw, hash, err := MintToken()
	if err != nil {
		t.Fatalf("MintToken err: %v", err)
	}
	if rehash := HashToken(raw); rehash != hash {
		t.Fatalf("HashToken(raw) = %q; want %q", rehash, hash)
	}
}

// TestValidatePrefixOK + TestValidatePrefixMismatch document the
// happy/sad split for capability tokens that the parser must classify
// before lookup (PAT vs MCP, etc.).
func TestValidatePrefixOK(t *testing.T) {
	if err := ValidatePrefix("pat_abcdef", PrefixPAT); err != nil {
		t.Fatalf("ValidatePrefix returned err on matching prefix: %v", err)
	}
}

func TestValidatePrefixMismatch(t *testing.T) {
	if err := ValidatePrefix("mcp_abcdef", PrefixPAT); err == nil {
		t.Fatal("ValidatePrefix returned nil on mismatched prefix")
	}
}

// TestValidatePrefixEmpty asserts an empty token cannot pass any
// prefix check. Without this, an injection that strips the auth header
// could otherwise round-trip past ValidatePrefix into the lookup path.
func TestValidatePrefixEmpty(t *testing.T) {
	if err := ValidatePrefix("", PrefixPAT); err == nil {
		t.Fatal("ValidatePrefix returned nil on empty token")
	}
}
