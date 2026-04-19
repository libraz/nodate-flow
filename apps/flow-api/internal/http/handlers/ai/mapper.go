package ai

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// nullableString returns a sql.NullString that is Valid only when s is non-empty.
func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// maskKey renders a provider key as "***<suffix>" for safe display.
//
// The provider prefix is intentionally NOT echoed: the secret-leak probe
// rejects any response containing a known provider prefix (e.g. "sk-ant-"),
// so we mask with a fixed "***" placeholder and only reveal the suffix.
// The prefix argument is accepted for call-site symmetry but ignored.
func maskKey(_, suffix string) string {
	return "***" + suffix
}

func rowToProvider(r generated.ListProvidersForWorkspaceRow) Provider {
	return Provider{
		ID:           r.PublicID.String(),
		Kind:         string(r.Kind),
		Name:         r.Name,
		BaseURL:      nullStr(r.BaseUrl),
		DefaultModel: nullStr(r.DefaultModel),
		APIKeyMasked: maskKey(r.ApiKeyPrefix, r.ApiKeySuffix),
		UpdatedAt:    nullTimeUnix(r.UpdatedAt),
		CreatedAt:    r.CreatedAt.Unix(),
	}
}

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64
