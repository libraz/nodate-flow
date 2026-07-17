package mcp

// MCP token scope vocabulary.
//
// Scopes are coarse access *tiers*, not per-tool or per-resource
// capabilities. The vocabulary is a small, closed set so the surface stays
// auditable; per-resource least privilege is a deliberate future extension
// enforced by the resolvers in acl.go, not by the scope string.
const (
	// ScopeReadWorkspace grants invoking read-only tools across the
	// workspace the token is bound to.
	ScopeReadWorkspace = "read:workspace"
	// ScopeWriteWorkspace grants invoking every mutating tool; it widens to
	// ScopeReadWorkspace.
	ScopeWriteWorkspace = "write:workspace"
)

// SupportedScopes is the canonical, closed allowlist of MCP token access
// tiers. It is the single source of truth shared by the issuance handler
// (which rejects any requested scope outside this set) and by
// [session.hasScope] (which only widens within it). Keep every scope the
// system honors here and nowhere else so the two paths cannot drift.
var SupportedScopes = []string{ScopeReadWorkspace, ScopeWriteWorkspace}

// IsSupportedScope reports whether raw names a recognized MCP token scope.
// Unknown scopes must be rejected at issuance rather than silently stored,
// since a stored-but-unmatched scope is confusing dead configuration.
func IsSupportedScope(raw string) bool {
	switch raw {
	case ScopeReadWorkspace, ScopeWriteWorkspace:
		return true
	default:
		return false
	}
}
