// Package signals contains Huma operation handlers and chi handlers for the
// /signals and /webhooks/* endpoints. Webhook handlers are intentionally
// implemented at the chi layer (not Huma) so they can verify the raw
// request body before unmarshalling.
package signals

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Audit records audit log entries. Nil-safe.
	Audit *audit.Recorder

	// GhWebhookSecret is the shared secret used to verify inbound
	// GitHub webhook signatures.
	GhWebhookSecret string
	// SlackSigningSecret is the v0 signing secret used to verify
	// inbound Slack event signatures.
	SlackSigningSecret string
	// GoogleChannelToken is the per-channel shared secret Google Drive
	// echoes back via X-Goog-Channel-Token on every push notification.
	GoogleChannelToken string
	// DefaultWorkspaceID is the workspace public id (UUID v7) that
	// inbound webhook signals are routed to.
	DefaultWorkspaceID string
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// Signal is the public DTO for a signals row.
type Signal struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"taskId,omitempty"`
	Source     string          `json:"source"`
	Kind       string          `json:"kind"`
	ExternalID string          `json:"externalId,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	ReceivedAt time.Time       `json:"receivedAt"`
	CreatedAt  time.Time       `json:"createdAt"`
}

// CreateInput is the body for POST /signals.
type CreateInput struct {
	Body struct {
		WorkspaceID string          `json:"workspaceId" doc:"Workspace public id (UUID v7)"`
		Source      string          `json:"source" enum:"manual,github,slack,email,webhook"`
		Kind        string          `json:"kind" minLength:"1" maxLength:"255"`
		ExternalID  string          `json:"externalId,omitempty" maxLength:"255"`
		TaskID      string          `json:"taskId,omitempty" doc:"Optional task public id to attach to"`
		Payload     json.RawMessage `json:"payload,omitempty"`
	}
}

// CreateOutput is the response for POST /signals.
type CreateOutput struct {
	Body Signal
}
