package signals

import "database/sql"

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
