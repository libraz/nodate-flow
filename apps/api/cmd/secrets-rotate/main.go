// Command secrets-rotate re-encrypts all stored ai_providers.api_key_ciphertext
// rows from an old NF_SECRET_KEY to a new NF_SECRET_KEY_NEW. Concrete DB
// wiring lands alongside the production rollout; the scaffold here proves
// the import surface and double-cipher construction.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	oldRaw := os.Getenv("NF_SECRET_KEY")
	newRaw := os.Getenv("NF_SECRET_KEY_NEW")
	if oldRaw == "" || newRaw == "" {
		logger.Error("NF_SECRET_KEY and NF_SECRET_KEY_NEW must both be set")
		os.Exit(1)
	}

	// Build the old cipher from the current process env.
	oldCipher, err := crypto.NewFromEnv()
	if err != nil {
		logger.Error("old cipher init failed", "err", err)
		os.Exit(1)
	}

	// Swap the env so NewFromEnv produces the new cipher, then restore.
	if err := os.Setenv("NF_SECRET_KEY", newRaw); err != nil {
		logger.Error("setenv failed", "err", err)
		os.Exit(1)
	}
	newCipher, err := crypto.NewFromEnv()
	if err != nil {
		logger.Error("new cipher init failed", "err", err)
		os.Exit(1)
	}
	if err := os.Setenv("NF_SECRET_KEY", oldRaw); err != nil {
		logger.Error("setenv restore failed", "err", err)
		os.Exit(1)
	}

	_ = oldCipher
	_ = newCipher
	fmt.Fprintln(os.Stderr, "secrets-rotate: scaffold OK; DB rewrite loop will be implemented with @sql once the rotation runbook is finalised")
}
