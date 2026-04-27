package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordTotpEnrolledAudit_EmitsExpectedEntry asserts the helper
// emits the canonical "auth.totp_enrolled" action against the actor.
func TestRecordTotpEnrolledAudit_EmitsExpectedEntry(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	recordTotpEnrolledAudit(context.Background(), Deps{Audit: sink}, 7)

	entries := sink.snapshot()
	require.Len(t, entries, 1)
	got := entries[0]
	assert.Equal(t, "auth.totp_enrolled", got.Action)
	assert.Equal(t, uint32(7), got.ActorID)
	assert.Equal(t, "user", got.ResourceType)
	assert.Empty(t, got.Metadata, "enrollment carries no metadata to leak the new secret")
}

// TestRecordTotpEnrolledAudit_NilSinkIsSafe asserts the helper does not
// panic when audit is unconfigured.
func TestRecordTotpEnrolledAudit_NilSinkIsSafe(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		recordTotpEnrolledAudit(context.Background(), Deps{Audit: nil}, 1)
	})
}

// TestRecordTotpDisabledAudit_EmitsExpectedEntry asserts the helper
// emits the canonical "auth.totp_disabled" action.
func TestRecordTotpDisabledAudit_EmitsExpectedEntry(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	recordTotpDisabledAudit(context.Background(), Deps{Audit: sink}, 9)

	entries := sink.snapshot()
	require.Len(t, entries, 1)
	got := entries[0]
	assert.Equal(t, "auth.totp_disabled", got.Action)
	assert.Equal(t, uint32(9), got.ActorID)
	assert.Equal(t, "user", got.ResourceType)
}

// TestRecordTotpDisabledAudit_NilSinkIsSafe asserts the helper does not
// panic when audit is unconfigured.
func TestRecordTotpDisabledAudit_NilSinkIsSafe(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		recordTotpDisabledAudit(context.Background(), Deps{Audit: nil}, 1)
	})
}

// TestRecordPasswordChangedAudit_CarriesRevokedSessionIDs asserts the
// helper attaches the list of revoked session public ids to the audit
// entry's metadata. This is the artefact investigators use to confirm
// that a stolen refresh token was killed at the moment the password
// rotated.
func TestRecordPasswordChangedAudit_CarriesRevokedSessionIDs(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	revoked := []string{
		"01961234-5678-7000-8000-aaaaaaaaaaaa",
		"01961234-5678-7000-8000-bbbbbbbbbbbb",
	}
	recordPasswordChangedAudit(context.Background(), Deps{Audit: sink}, 11, revoked)

	entries := sink.snapshot()
	require.Len(t, entries, 1)
	got := entries[0]
	assert.Equal(t, "auth.password_changed", got.Action)
	assert.Equal(t, uint32(11), got.ActorID)
	assert.Equal(t, "user", got.ResourceType)

	rawList, ok := got.Metadata["revoked_session_ids"]
	require.True(t, ok, "metadata must contain revoked_session_ids array")
	gotList, ok := rawList.([]string)
	require.True(t, ok, "revoked_session_ids must be []string for stable downstream parsing")
	assert.Equal(t, revoked, gotList,
		"helper must forward the exact revoked-session list verbatim")
}

// TestRecordPasswordChangedAudit_EmptyListIsRecorded asserts that even
// when no other sessions were active, an empty list is still recorded
// rather than dropped — distinguishing "no other devices" from "we
// forgot to record this side effect".
func TestRecordPasswordChangedAudit_EmptyListIsRecorded(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	recordPasswordChangedAudit(context.Background(), Deps{Audit: sink}, 1, []string{})

	entries := sink.snapshot()
	require.Len(t, entries, 1)
	rawList := entries[0].Metadata["revoked_session_ids"]
	got, ok := rawList.([]string)
	require.True(t, ok)
	assert.Empty(t, got)
}

// TestRecordPasswordChangedAudit_NilSinkIsSafe asserts the helper does
// not panic when audit is unconfigured.
func TestRecordPasswordChangedAudit_NilSinkIsSafe(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		recordPasswordChangedAudit(context.Background(), Deps{Audit: nil}, 1, []string{"x"})
	})
}
