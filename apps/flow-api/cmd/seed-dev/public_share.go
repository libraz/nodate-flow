package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// findShareIDByTitle matches seed shares by (workspace_id, title) so
// re-runs find the prior row without exposing the plaintext token.
func findShareIDByTitle(ctx context.Context, db *sql.DB, wsID uint32, title string) (uint32, error) {
	var id uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_public_shares
		 WHERE workspace_id = ? AND title = ? AND enabled = TRUE
		 ORDER BY id ASC LIMIT 1`,
		wsID, title,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

func ensurePublicShare(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, ownerID uint32, title, description string, logger *slog.Logger) (uint32, error) {
	if existing, err := findShareIDByTitle(ctx, db, wsID, title); err != nil {
		return 0, err
	} else if existing > 0 {
		logger.Info("public share exists", "id", existing, "title", title)
		return existing, nil
	}
	token, err := mintShareToken()
	if err != nil {
		return 0, fmt.Errorf("mint token: %w", err)
	}
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	id, err := cq.CreatePublicShare(ctx, calendar.CreatePublicShareParams{
		PublicID:            types.New(),
		WorkspaceID:         wsID,
		CreatedByUserID:     sql.NullInt32{Int32: int32(ownerID), Valid: true}, //#nosec G115 -- owner id sourced from seed flow, fits int32
		TokenHash:           tokenHash,
		Title:               title,
		Description:         sql.NullString{String: description, Valid: description != ""},
		Timezone:            "UTC",
		ShowHolidaysCountry: sql.NullString{String: "JP", Valid: true},
	})
	if err != nil {
		return 0, err
	}
	logger.Info("created public share", "id", id, "title", title, "token", token)
	return uint32(id), nil //#nosec G115 -- LastInsertId for calendar_public_shares.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureShareEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, shareID, eventID uint32, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_public_share_events
		 WHERE share_id = ? AND event_id = ? AND enabled = TRUE LIMIT 1`,
		shareID, eventID,
	).Scan(&existing)
	if err == nil {
		logger.Info("share event exists", "share_id", shareID, "event_id", eventID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := cq.AttachEventToShare(ctx, calendar.AttachEventToShareParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		ShareID:     shareID,
		EventID:     eventID,
		SortWeight:  0,
	}); err != nil {
		return err
	}
	logger.Info("attached event to share", "share_id", shareID, "event_id", eventID)
	return nil
}

// mintShareToken mirrors the handler's token minting (24 random bytes
// -> RawURLEncoding -> SHA-256) so seeded shares look like real ones.
func mintShareToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
