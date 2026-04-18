// Package eventbus provides a minimal event appender for the time-api
// service. It inserts rows directly into the shared events table so that
// flow-api's SSE notifier and AI agent subscriptions pick them up.
package eventbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
)

// Append inserts a calendar event into the shared events table.
func Append(ctx context.Context, db *sql.DB, workspaceID uint32, eventType string, actorUserID *uint32, payload map[string]any) error {
	publicID := types.New()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		payloadJSON = []byte("{}")
	}

	var actorID *uint32
	if actorUserID != nil {
		actorID = actorUserID
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO events (public_id, workspace_id, actor_user_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		publicID, workspaceID, actorID, eventType, payloadJSON, time.Now(),
	)
	return err
}
