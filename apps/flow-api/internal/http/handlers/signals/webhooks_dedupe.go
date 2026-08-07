package signals

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// externalIDMaxLen mirrors the signals.external_id column width
// (VARCHAR(255) in sql/flow/tables/signals.sql).
const externalIDMaxLen = 255

// dedupeKey wraps a provider-supplied delivery identifier for
// signals.external_id. An empty or over-long value yields a NULL
// external_id, which takes the row out of the
// (workspace_id, source, external_id) unique key so it is stored without
// dedupe.
//
// NULL rather than a truncated value is deliberate: truncation collapses
// two distinct deliveries onto one key, and the second one is then
// silently dropped by INSERT IGNORE. A duplicate row is recoverable
// downstream; a dropped signal is not.
func dedupeKey(s string) sql.NullString {
	if s == "" || len(s) > externalIDMaxLen {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// respondIfDuplicate handles the INSERT IGNORE no-op that InsertSignal
// reports as LastInsertId() == 0: the delivery collided with an existing
// row on (workspace_id, source, external_id) and nothing was written. The
// public id the caller minted therefore matches no persisted row, so it
// must not be returned, and the judge must not be woken for a signal that
// was already judged on first delivery.
//
// Returns true when it has written the response and the caller must
// return immediately; false leaves the caller on its normal path.
//
// A duplicate is acknowledged with the same 202 as a genuine insert —
// every provider here retries on non-2xx, and a redelivery would collide
// again forever — but the body carries the EXISTING row's public id and
// `duplicate: true` so the receiver never claims to have created a signal
// it did not create.
//
// LastInsertId() == 0 with no colliding row means the INSERT IGNORE
// swallowed a genuine failure. That is answered with 500 so the provider
// retries the whole path instead of being told a delivery landed that in
// fact did not.
func respondIfDuplicate(
	ctx context.Context,
	w http.ResponseWriter,
	deps Deps,
	wsID uint32,
	source generated.SignalsSource,
	ext sql.NullString,
	signalInternalID int64,
	handler string,
) bool {
	if signalInternalID != 0 {
		return false
	}
	existing, found, err := resolveSignalByDedupeKey(ctx, deps.DB, wsID, source, ext)
	if err != nil {
		slog.ErrorContext(ctx, "webhook: dedupe key lookup failed",
			slog.Any("error", err),
			slog.String("handler", handler),
		)
		writeError(w, apierrors.InternalUnexpected)
		return true
	}
	if !found {
		slog.ErrorContext(ctx, "webhook: signal insert wrote no row",
			slog.String("handler", handler),
			slog.Bool("external_id_present", ext.Valid),
		)
		writeError(w, apierrors.InternalUnexpected)
		return true
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":        existing.String(),
		"duplicate": true,
	})
	return true
}
