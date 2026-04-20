// Package reactions contains Huma operation handlers for task-scoped
// reaction CRUD. Reactions are per-user emoji annotations on tasks.
package reactions

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// Reaction is the public DTO for a reaction row.
type Reaction struct {
	ID              string `json:"id" doc:"Reaction public id (UUID v7)"`
	Emoji           string `json:"emoji"`
	UserID          string `json:"userId"`
	UserDisplayName string `json:"userDisplayName"`
	CreatedAt       int64  `json:"createdAt"`
}

// ---- Create ----

// CreateReactionBody is the JSON body for POST /tasks/{id}/reactions.
type CreateReactionBody struct {
	Emoji string `json:"emoji" minLength:"1" maxLength:"32"`
}

// CreateReactionInput is the request for POST /tasks/{id}/reactions.
type CreateReactionInput struct {
	ID   string `path:"id" doc:"Task public id (UUID v7)"`
	Body CreateReactionBody
}

// CreateReactionOutput is the response for POST /tasks/{id}/reactions.
type CreateReactionOutput struct {
	Body Reaction
}

// ---- List ----

// ListReactionsInput is the query for GET /tasks/{id}/reactions.
type ListReactionsInput struct {
	ID string `path:"id" doc:"Task public id (UUID v7)"`
}

// ListReactionsBody is the response payload for GET /tasks/{id}/reactions.
type ListReactionsBody struct {
	Reactions []Reaction `json:"reactions"`
}

// ListReactionsOutput is the response for GET /tasks/{id}/reactions.
type ListReactionsOutput struct {
	Body ListReactionsBody
}

// ---- Delete ----

// DeleteReactionInput is the path for DELETE /tasks/{id}/reactions/{reactionId}.
type DeleteReactionInput struct {
	ID         string `path:"id" doc:"Task public id (UUID v7)"`
	ReactionID string `path:"reactionId" doc:"Reaction public id (UUID v7)"`
}

// DeleteReactionOutput is the response for DELETE /tasks/{id}/reactions/{reactionId}.
type DeleteReactionOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
