package e2e

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// snoozeDeadlineCode is the answer a snooze with no usable deadline gets.
const snoozeDeadlineCode = "WS.INTAKE.SNOOZE_DEADLINE_REQUIRED"

// farFutureDeadline is a deadline no test run can be past: 2100-01-01.
const farFutureDeadline int64 = 4_102_444_800

// createIntakeItem drops one item on the workspace queue and returns its
// public id.
func createIntakeItem(t *testing.T, tt *helpers.TestTenant, title string) string {
	t.Helper()
	var item struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake",
		tt.AccessToken, map[string]any{"title": title}, &item)
	require.NotEmpty(t, item.ID)
	return item.ID
}

// intakeRow reads the two columns a triage decision writes, straight from
// the row. The response DTO omits an absent snoozeUntil, so a handler that
// stored nothing and a handler that stored the wrong thing can look alike
// from the body alone.
func intakeRow(t *testing.T, itemID string) (status string, snoozeUntil sql.NullTime) {
	t.Helper()
	require.NoError(t, testDB.QueryRow(
		`SELECT triage_status, snooze_until FROM intake_items
		  WHERE public_id = UUID_TO_BIN(?, 0)`, itemID).Scan(&status, &snoozeUntil))
	return status, snoozeUntil
}

// triageIntake sends one triage request and returns the status code and body.
func triageIntake(t *testing.T, tt *helpers.TestTenant, itemID string, body map[string]any) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake/"+itemID,
		tt.AccessToken, body)
}

// snoozeWithoutDeadlineCases are the request bodies that ask for a snooze
// the queue could never undo: no deadline at all, and two that name an
// instant already in the past by construction.
func snoozeWithoutDeadlineCases() []struct {
	name string
	body map[string]any
} {
	return []struct {
		name string
		body map[string]any
	}{
		{"no deadline", map[string]any{"status": "snoozed"}},
		{"zero deadline", map[string]any{"status": "snoozed", "snoozeUntil": 0}},
		{"negative deadline", map[string]any{"status": "snoozed", "snoozeUntil": -1}},
	}
}

// TestIntakeTriageRequiresSnoozeDeadline pins the refusal on the REST
// triage route.
//
// Nothing resurfaces a snoozed item: the intake queue is filtered on
// triage_status and no job scans snooze_until. An item accepted as snoozed
// with a NULL deadline therefore left the pending list permanently while
// the route answered 200 — the caller was told it would come back. A
// non-positive deadline is the same outcome by a different route, since
// epoch or earlier is not a date anything waits for.
func TestIntakeTriageRequiresSnoozeDeadline(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	itemID := createIntakeItem(t, tt, "Needs a deadline")

	for _, tc := range snoozeWithoutDeadlineCases() {
		t.Run(tc.name, func(t *testing.T) {
			status, body := triageIntake(t, tt, itemID, tc.body)
			require.Equalf(t, http.StatusUnprocessableEntity, status,
				"%s must be refused; body=%s", tc.name, string(body))
			require.Equalf(t, snoozeDeadlineCode, decodeErrorCode(t, body),
				"%s must surface the snooze-deadline code; body=%s", tc.name, string(body))

			// The refusal has to leave the queue as it was. A handler that
			// wrote the status and then complained would have already lost
			// the item, which is the whole harm.
			triage, snooze := intakeRow(t, itemID)
			assert.Equalf(t, "pending", triage, "%s must leave the item pending", tc.name)
			assert.Falsef(t, snooze.Valid, "%s must not write a deadline", tc.name)
		})
	}
}

// TestIntakeSnoozeCheckOutranksTheItemLookup pins where the check sits.
//
// It is argument validation, so it runs before the item is read: a
// malformed call must not depend on the item existing, and a caller who
// sent a snooze with no deadline is told that rather than being sent
// looking for a row. The id below is a well-formed UUID that names no row,
// so a WS.INTAKE.NOT_FOUND here means the check drifted below the lookup.
func TestIntakeSnoozeCheckOutranksTheItemLookup(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	missingID := types.New().UUID().String()

	// Both faults at once: the argument check wins.
	status, body := triageIntake(t, tt, missingID, map[string]any{"status": "snoozed"})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"a deadline-less snooze must be refused before the item lookup, body=%s", string(body))
	require.Equal(t, snoozeDeadlineCode, decodeErrorCode(t, body),
		"a deadline-less snooze outranks an unresolvable item, body=%s", string(body))

	// Only the item is wrong: the lookup gets to answer, which is what
	// makes the assertion above mean the argument check ran first.
	status, body = triageIntake(t, tt, missingID, map[string]any{
		"status":      "snoozed",
		"snoozeUntil": farFutureDeadline,
	})
	require.Equal(t, http.StatusNotFound, status,
		"a well-formed snooze of a missing item must be a not-found, body=%s", string(body))
	require.Equal(t, "WS.INTAKE.NOT_FOUND", decodeErrorCode(t, body),
		"a missing item must surface WS.INTAKE.NOT_FOUND, body=%s", string(body))
}

// TestIntakeTriageAcceptsSnoozeWithDeadline is the other side of the rule:
// tightening it must not have closed the case it exists to serve.
func TestIntakeTriageAcceptsSnoozeWithDeadline(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	itemID := createIntakeItem(t, tt, "Snooze me until next year")

	status, body := triageIntake(t, tt, itemID, map[string]any{
		"status":      "snoozed",
		"snoozeUntil": farFutureDeadline,
	})
	require.Equal(t, http.StatusOK, status, "a snooze with a deadline must succeed; body=%s", string(body))

	var out struct {
		TriageStatus string `json:"triageStatus"`
		SnoozeUntil  *int64 `json:"snoozeUntil"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, "snoozed", out.TriageStatus)
	require.NotNil(t, out.SnoozeUntil, "the response must carry the deadline back")
	assert.Equal(t, farFutureDeadline, *out.SnoozeUntil)

	triage, snooze := intakeRow(t, itemID)
	assert.Equal(t, "snoozed", triage)
	require.True(t, snooze.Valid, "the deadline must have landed in the row")
	assert.Equal(t, farFutureDeadline, snooze.Time.Unix())
}

// TestIntakeTriageWithoutSnoozeStatusNeedsNoDeadline covers the statuses
// the rule does not apply to, and pins what happens to a deadline sent
// with one of them.
//
// A non-snooze triage takes no deadline and needs none. When one is sent
// anyway it is dropped rather than stored: only a snoozed item has
// something to resurface from, and the update writes snooze_until
// unconditionally. That is the current contract on both transports; the
// assertion is here so a change to it is a deliberate one.
func TestIntakeTriageWithoutSnoozeStatusNeedsNoDeadline(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	for _, status := range []string{"accepted", "rejected", "duplicate"} {
		t.Run(status, func(t *testing.T) {
			itemID := createIntakeItem(t, tt, "Triaged as "+status)
			code, body := triageIntake(t, tt, itemID, map[string]any{"status": status})
			require.Equalf(t, http.StatusOK, code,
				"%s must not require a deadline; body=%s", status, string(body))

			triage, snooze := intakeRow(t, itemID)
			assert.Equal(t, status, triage)
			assert.False(t, snooze.Valid, "a non-snooze triage stores no deadline")
		})
	}

	// A deadline offered alongside a non-snooze status is discarded.
	itemID := createIntakeItem(t, tt, "Accepted with a stray deadline")
	code, body := triageIntake(t, tt, itemID, map[string]any{
		"status":      "accepted",
		"snoozeUntil": farFutureDeadline,
	})
	require.Equal(t, http.StatusOK, code, "body=%s", string(body))

	var out struct {
		SnoozeUntil *int64 `json:"snoozeUntil"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Nil(t, out.SnoozeUntil,
		"a deadline sent with a non-snooze status is not reported back")

	triage, snooze := intakeRow(t, itemID)
	assert.Equal(t, "accepted", triage)
	assert.False(t, snooze.Valid,
		"a deadline sent with a non-snooze status is not stored")
}

// TestIntakeSnoozeDeadlineRuleMatchesMCP pins the two write routes to one
// answer for the same inputs.
//
// The rule lived on the agent surface alone: MCP refused a deadline-less
// snooze while REST wrote the NULL and reported success, so which
// transport a caller used decided whether their item survived. Both halves
// run here — the refusals and the acceptance — because a split reopens as
// easily by one side loosening as by the other tightening.
//
// The codes differ by transport and that is not the parity being pinned:
// MCP answers every malformed argument with MCP.TOOL.ARGUMENTS_INVALID,
// REST names the field's own catalogue code. What has to match is the
// verdict on each input, and what lands in the row when it is accepted.
func TestIntakeSnoozeDeadlineRuleMatchesMCP(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	for _, tc := range snoozeWithoutDeadlineCases() {
		t.Run(tc.name, func(t *testing.T) {
			restItem := createIntakeItem(t, tt, "REST "+tc.name)
			status, body := triageIntake(t, tt, restItem, tc.body)
			require.Equalf(t, http.StatusUnprocessableEntity, status,
				"REST must refuse %s; body=%s", tc.name, string(body))
			require.Equalf(t, snoozeDeadlineCode, decodeErrorCode(t, body),
				"REST must name the snooze-deadline code for %s; body=%s", tc.name, string(body))

			mcpItem := createIntakeItem(t, tt, "MCP "+tc.name)
			args := map[string]any{"intakeItemId": mcpItem}
			for k, v := range tc.body {
				args[k] = v
			}
			code := mcpToolErrorCode(t, tt, "triage_intake_item", args)
			require.NotEmptyf(t, code, "MCP must refuse %s too", tc.name)

			// Neither transport may have moved either item off the queue.
			for label, id := range map[string]string{"REST": restItem, "MCP": mcpItem} {
				triage, snooze := intakeRow(t, id)
				assert.Equalf(t, "pending", triage, "%s left the item triaged for %s", label, tc.name)
				assert.Falsef(t, snooze.Valid, "%s wrote a deadline for %s", label, tc.name)
			}
		})
	}

	// A snooze that carries a real deadline is accepted by both, and both
	// store it. Without this the test would also pass on a pair of
	// transports that refuse every snooze.
	restItem := createIntakeItem(t, tt, "REST valid snooze")
	status, body := triageIntake(t, tt, restItem, map[string]any{
		"status":      "snoozed",
		"snoozeUntil": farFutureDeadline,
	})
	require.Equal(t, http.StatusOK, status, "REST must accept a snooze with a deadline; body=%s", string(body))

	mcpItem := createIntakeItem(t, tt, "MCP valid snooze")
	out := mcpTool(t, tt, "triage_intake_item", map[string]any{
		"intakeItemId": mcpItem,
		"status":       "snoozed",
		"snoozeUntil":  farFutureDeadline,
	})
	assert.Contains(t, out, "\"ok\":true", "MCP must accept the same snooze: %s", out)

	for label, id := range map[string]string{"REST": restItem, "MCP": mcpItem} {
		triage, snooze := intakeRow(t, id)
		assert.Equalf(t, "snoozed", triage, "%s must have snoozed the item", label)
		require.Truef(t, snooze.Valid, "%s must have stored a deadline", label)
		assert.Equalf(t, farFutureDeadline, snooze.Time.Unix(),
			"%s must store the deadline it was given", label)
	}
}

// TestIntakeSnoozeDeadlineIsNotSchemaValidated states which layer answers.
//
// The bound is conditional on the status, which a struct tag cannot
// express, so a `minimum` on the field would split one malformed snooze
// into two answers: a schema refusal for a non-positive deadline and the
// catalogue code for a missing one. The handler owns the whole rule, and
// this pins that a caller sees one code for the class.
func TestIntakeSnoozeDeadlineIsNotSchemaValidated(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	itemID := createIntakeItem(t, tt, "One code for the class")

	codes := make(map[string]struct{})
	for _, tc := range snoozeWithoutDeadlineCases() {
		_, body := triageIntake(t, tt, itemID, tc.body)
		codes[decodeErrorCode(t, body)] = struct{}{}
	}
	require.Len(t, codes, 1, "every deadline-less snooze must answer with the same code: %v", codes)
	_, ok := codes[snoozeDeadlineCode]
	require.True(t, ok, "and that code is %s: %v", snoozeDeadlineCode, codes)
}
