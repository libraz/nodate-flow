package workspace

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// senderType is the interface an outbound-mail dependency satisfies.
var senderType = reflect.TypeOf((*email.Sender)(nil)).Elem()

// hasMailer reports whether any field of the struct type could deliver
// an email.
func hasMailer(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Type == senderType {
			return true
		}
	}
	return false
}

// TestAddMemberCannotMail pins what POST /workspaces/{wsId}/members
// really is.
//
// It grants a seat outright: no token, no email, no acceptance. The
// bundle it is handed carries no mail sender and no web origin, so it
// could not deliver an invitation even if it wanted to — which is the
// point, because the operation used to be documented as "sends a
// workspace invite to the supplied email", and an integrator following
// that description was silently adding unverified addresses to a
// workspace instead of asking them to join.
//
// The consent-based path is CreateInvite, whose bundle does carry both.
func TestAddMemberCannotMail(t *testing.T) {
	t.Parallel()

	deps := reflect.TypeOf(Deps{})
	assert.False(t, hasMailer(deps),
		"Deps feeds AddMember; a mail sender here would mean the add-member path can send invitations")

	inviteDeps := reflect.TypeOf(InviteDeps{})
	require.True(t, hasMailer(inviteDeps),
		"CreateInvite is the path that mails a token; without a sender there is no consent-based invite at all")
	_, hasWebURL := inviteDeps.FieldByName("WebURL")
	assert.True(t, hasWebURL, "the invite mail needs an origin to build its link against")
}
