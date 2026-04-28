package token

import (
	"errors"
	"strings"
)

// Centralised user-visible prefixes for tokens that the parser actually
// branches on (PAT vs MCP vs refresh, magic-link, invite, share). Keep
// every prefix string declared exactly once in this file so a new
// resource type cannot collide with an existing namespace by accident.
const (
	// PrefixMagicLink prefixes auth magic-link tokens delivered via
	// email (sign-in / passwordless flows).
	PrefixMagicLink = "ml_"

	// PrefixInvite prefixes workspace + calendar event invite tokens.
	PrefixInvite = "inv_"

	// PrefixPAT prefixes personal access tokens (long-lived bearer
	// credentials minted via /me/tokens).
	PrefixPAT = "pat_"

	// PrefixMCP prefixes MCP transport bearer tokens.
	PrefixMCP = "mcp_"

	// PrefixRefresh prefixes refresh tokens issued alongside a session
	// access token.
	PrefixRefresh = "rfr_"

	// PrefixShare prefixes public-share URL capability tokens.
	PrefixShare = "pub_"
)

// ErrPrefixMismatch is returned by ValidatePrefix when the supplied
// token does not start with the expected prefix. The error is
// deliberately opaque — handlers translate it to a public apierror
// code (typically *_TOKEN_INVALID) so the wire shape never reveals
// which prefix was expected.
var ErrPrefixMismatch = errors.New("token: prefix mismatch")

// ValidatePrefix reports whether token starts with expectedPrefix.
// Returns ErrPrefixMismatch when the prefix is missing or differs;
// returns nil otherwise. expectedPrefix should be one of the Prefix*
// constants in this package.
func ValidatePrefix(token, expectedPrefix string) error {
	if !strings.HasPrefix(token, expectedPrefix) {
		return ErrPrefixMismatch
	}
	return nil
}
