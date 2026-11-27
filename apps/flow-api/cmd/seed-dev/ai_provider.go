package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// seedAIProviderKind / seedAIProviderName / seedAIModelName are the
// defaults used by ensureAIProviderAndModel. They are intentionally
// scoped to this file so ops tooling changes do not ripple into the
// real create-provider handler.
const (
	seedAIProviderKind = "anthropic"
	seedAIProviderName = "Anthropic (seed placeholder)"
	seedAIModelName    = "claude-sonnet-5-5"
)

// ensureAIProviderAndModel inserts an ai_providers row and a matching
// ai_models row so the AI-agents settings page has something to bind
// against on a freshly seeded dev DB. The api_key_ciphertext is a
// deterministic-looking random blob and the prefix/suffix flag the row
// as a seed placeholder. Rotating the key via PATCH /ai/providers turns
// the provider into a real one. Idempotent on (workspace_id, kind, name).
func ensureAIProviderAndModel(ctx context.Context, db *sql.DB, wsID uint32, logger *slog.Logger) error {
	var providerID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM ai_providers
		 WHERE workspace_id = ? AND kind = ? AND name = ? AND enabled = TRUE
		 LIMIT 1`,
		wsID, seedAIProviderKind, seedAIProviderName,
	).Scan(&providerID)
	switch {
	case err == nil:
		logger.Info("ai provider exists", "provider_id", providerID)
	case errors.Is(err, sql.ErrNoRows):
		pub := types.New()
		// 32 random bytes stand in for AES-GCM output. The provider row
		// will not be usable for real LLM dispatch until an operator
		// rotates the key via the PATCH endpoint.
		ct := make([]byte, 32)
		if _, err := rand.Read(ct); err != nil {
			return fmt.Errorf("rand ct: %w", err)
		}
		res, err := db.ExecContext(ctx,
			`INSERT INTO ai_providers (
				public_id, workspace_id, kind, name,
				api_key_ciphertext, api_key_prefix, api_key_suffix,
				default_model
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			pub, wsID, seedAIProviderKind, seedAIProviderName,
			ct, "sk-SEED-", "SEED", seedAIModelName,
		)
		if err != nil {
			return fmt.Errorf("insert ai provider: %w", err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("ai provider last id: %w", err)
		}
		providerID = uint32(newID) //#nosec G115 -- LastInsertId for ai_providers.id (BIGINT UNSIGNED), fits uint32 in dev seed
		logger.Info("created ai provider", "id", providerID, "kind", seedAIProviderKind,
			"note", "api_key_ciphertext is a seed placeholder; rotate via PATCH before real use")
	default:
		return fmt.Errorf("lookup ai provider: %w", err)
	}

	// ai_models row matching the provider's default_model. Unique key is
	// (provider_id, name) so re-runs become no-ops.
	var modelID uint32
	err = db.QueryRowContext(ctx,
		`SELECT id FROM ai_models
		 WHERE provider_id = ? AND name = ? AND enabled = TRUE
		 LIMIT 1`,
		providerID, seedAIModelName,
	).Scan(&modelID)
	switch {
	case err == nil:
		logger.Info("ai model exists", "model_id", modelID)
		return nil
	case errors.Is(err, sql.ErrNoRows):
		pub := types.New()
		res, err := db.ExecContext(ctx,
			`INSERT INTO ai_models (
				public_id, workspace_id, provider_id, name, display_name,
				context_window, max_output_tokens,
				input_price_micro_usd_per_mtok, output_price_micro_usd_per_mtok,
				supports_tools, supports_vision, enabled
			) VALUES (?, ?, ?, ?, ?, 1000000, 128000, 2000000, 10000000, TRUE, TRUE, TRUE)`,
			pub, wsID, providerID, seedAIModelName, seedAIModelName,
		)
		if err != nil {
			return fmt.Errorf("insert ai model: %w", err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("ai model last id: %w", err)
		}
		logger.Info("created ai model", "id", newID, "name", seedAIModelName)
		return nil
	default:
		return fmt.Errorf("lookup ai model: %w", err)
	}
}
