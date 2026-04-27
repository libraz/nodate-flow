// Package integrations contains Huma handlers for the personal
// integrations area under /me/integrations and the public
// /oauth/callback/{provider} endpoint.
package integrations

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	integrationspkg "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
)

// HandlerQuerier is the narrow subset of [generated.Querier] that the
// integrations handlers need. Declared as an interface so unit tests
// can substitute a fake without standing up a real database, following
// the same pattern as [integrationspkg.LoaderQuerier].
type HandlerQuerier interface {
	ListUserIntegrations(ctx context.Context, userID uint32) ([]generated.ListUserIntegrationsRow, error)
	CreateOauthState(ctx context.Context, arg generated.CreateOauthStateParams) error
	ConsumeOauthState(ctx context.Context, state string) (generated.ConsumeOauthStateRow, error)
	DeleteOauthState(ctx context.Context, state string) error
	PurgeExpiredOauthStates(ctx context.Context) error
	UpsertUserIntegration(ctx context.Context, arg generated.UpsertUserIntegrationParams) (int64, error)
	FindUserIntegrationByPublicId(ctx context.Context, arg generated.FindUserIntegrationByPublicIdParams) (generated.FindUserIntegrationByPublicIdRow, error)
	FindUserIntegrationByUserProvider(ctx context.Context, arg generated.FindUserIntegrationByUserProviderParams) (generated.FindUserIntegrationByUserProviderRow, error)
	DeleteUserIntegration(ctx context.Context, arg generated.DeleteUserIntegrationParams) error
}

// Deps bundles the dependencies required by the integrations handlers.
type Deps struct {
	DB            *sql.DB
	Queries       HandlerQuerier
	Cipher        *crypto.Cipher
	Registry      *integrationspkg.Registry
	PublicBaseURL string
	WebBaseURL    string
	// Audit records audit log entries for connect/disconnect side
	// effects. Optional: nil disables audit logging.
	Audit audit.Sink
}

// ConnectionSummary is the public DTO for a user_integrations row.
type ConnectionSummary struct {
	ID                   string `json:"id" doc:"Connection public id (UUID v7)"`
	Provider             string `json:"provider" enum:"github,slack,google_calendar"`
	ExternalAccountID    string `json:"externalAccountId"`
	ExternalAccountLabel string `json:"externalAccountLabel"`
	Scopes               string `json:"scopes"`
	ConnectedAt          int64  `json:"connectedAt"`
	LastRefreshedAt      *int64 `json:"lastRefreshedAt,omitempty"`
	AccessTokenExpiresAt *int64 `json:"accessTokenExpiresAt,omitempty"`
}

// ProviderStatus is an entry in the provider catalog returned by
// GET /me/integrations. It lets the UI render every provider card
// (even the ones missing a server-side connection) and disable the
// Connect button for providers that are not configured.
type ProviderStatus struct {
	Provider   string             `json:"provider" enum:"github,slack,google_calendar"`
	Configured bool               `json:"configured" doc:"True when the server has credentials for this provider"`
	Connection *ConnectionSummary `json:"connection,omitempty"`
}

// ListIntegrationsOutput is the response for GET /me/integrations.
type ListIntegrationsOutput struct {
	Body struct {
		Providers []ProviderStatus `json:"providers"`
	}
}

// ConnectIntegrationInput is the request for POST /me/integrations/{provider}/connect.
type ConnectIntegrationInput struct {
	Provider string `path:"provider" enum:"github,slack,google_calendar"`
	Body     struct {
		RedirectTo string `json:"redirectTo,omitempty" maxLength:"512" doc:"Optional client-supplied return URL; defaults to the integrations settings page"`
	}
}

// ConnectIntegrationOutput is the response for the connect endpoint.
// The client navigates to AuthorizeURL; the provider will bounce
// the user to GET /oauth/callback/{provider} when consent finishes.
type ConnectIntegrationOutput struct {
	Body struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
}

// OAuthCallbackInput is the query for GET /oauth/callback/{provider}.
type OAuthCallbackInput struct {
	Provider string `path:"provider" enum:"github,slack,google_calendar"`
	Code     string `query:"code"`
	State    string `query:"state"`
	Error    string `query:"error,omitempty"`
}

// OAuthCallbackOutput is a 302 redirect back to the web app.
type OAuthCallbackOutput struct {
	Status   int
	Location string `header:"Location"`
}

// DisconnectInput is the request for DELETE /me/integrations/{id}.
type DisconnectInput struct {
	ID string `path:"id" doc:"Connection public id (UUID v7)"`
}

// DisconnectOutput is the response for the disconnect endpoint.
type DisconnectOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
