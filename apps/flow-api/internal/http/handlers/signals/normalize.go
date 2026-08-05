// Package signals — subject normalisation helpers shared by the manual
// ingestion handler (POST /signals) and the provider webhook adapters
// (GitHub / Slack / Google). Centralised here so every write path lands
// the same `(source, kind, subject_type, subject_id)` shape per ADR 0008
// D1.
package signals

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// resolveSubjectType picks the SignalsSubjectType for an insert.
//
// Precedence:
//  1. an explicit request-supplied SubjectType wins (caller knows best);
//  2. fall back to the kind's SubjectTypeDefault from signal_kinds/*.yaml
//     when the kind is in the registry;
//  3. fall back to subject_type='workspace' for legacy / unknown kinds
//     where no other subject is meaningful. This keeps webhook adapters
//     that emit free-form kinds (today: github issue/PR/commit events not
//     yet promoted to the registry) writing rows that satisfy the NOT NULL
//     constraint without claiming a more specific subject than they own.
//
// The returned value is always a non-empty enum member that satisfies the
// signals.subject_type NOT NULL constraint.
func resolveSubjectType(kind string, override string) generated.SignalsSubjectType {
	if override != "" {
		return generated.SignalsSubjectType(override)
	}
	if def, ok := signalkinds.Lookup(signalkinds.Kind(kind)); ok {
		return generated.SignalsSubjectType(def.SubjectTypeDefault)
	}
	return generated.SignalsSubjectTypeWorkspace
}

// subjectIDFor returns the sql.NullInt32 to put in signals.subject_id given
// the resolved subject type and any internal id the caller has already
// looked up. The bookkeeping rule (mirrors sql/flow/tables/signals.sql) is that
// subject_id stays NULL when subject_type=workspace, because workspace_id
// on the same row already owns the subject.
func subjectIDFor(subjectType generated.SignalsSubjectType, internalID int64) sql.NullInt32 {
	if subjectType == generated.SignalsSubjectTypeWorkspace || internalID == 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(internalID), Valid: true} //#nosec G115 -- subject_id is an internal row id (INT UNSIGNED), fits int32 within realistic deployments
}
