package authn

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// DeriveEd25519Key deterministically derives an Ed25519 private key from
// a master secret (typically NF_SECRET_KEY, 32+ bytes as hex or base64).
// The info string differentiates keys so the same master secret produces
// distinct keys for different services. Returns nil if secret is empty.
func DeriveEd25519Key(secret, info string) (ed25519.PrivateKey, error) {
	if secret == "" {
		return nil, nil
	}
	master, err := decodeMaster(secret)
	if err != nil {
		return nil, err
	}
	if len(master) < 32 {
		return nil, fmt.Errorf("authn: secret too short (%d bytes, need >= 32)", len(master))
	}
	r := hkdf.New(sha256.New, master, nil, []byte(info))
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(r, seed); err != nil {
		return nil, fmt.Errorf("authn: hkdf read: %w", err)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// decodeMaster accepts the master key as 64-char hex or standard base64.
func decodeMaster(raw string) ([]byte, error) {
	if len(raw) == 64 {
		if b, err := hex.DecodeString(raw); err == nil {
			return b, nil
		}
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("authn: NF_SECRET_KEY must be 32-byte hex or base64")
}
