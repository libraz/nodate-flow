package ai

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// nullableString returns a sql.NullString that is Valid only when s is non-empty.
func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// maskKey renders a provider key as "<prefix>...<suffix>" for safe display.
func maskKey(prefix, suffix string) string {
	return prefix + "..." + suffix
}

func rowToProvider(r generated.ListProvidersForWorkspaceRow) Provider {
	return Provider{
		ID:           r.PublicID.String(),
		Kind:         string(r.Kind),
		Name:         r.Name,
		BaseURL:      nullStr(r.BaseUrl),
		DefaultModel: nullStr(r.DefaultModel),
		APIKeyMasked: maskKey(r.ApiKeyPrefix, r.ApiKeySuffix),
		UpdatedAt:    nullTime(r.UpdatedAt),
		CreatedAt:    r.CreatedAt,
	}
}

// totalAsInt64 normalizes the COUNT(*) OVER() return type into int64.
func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}
