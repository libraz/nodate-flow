// Package handlerutil provides shared helper functions used across multiple
// handler packages. These small utilities avoid duplicating conversion and
// detection logic in every feature handler.
package handlerutil

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/problem"
)

// ProblemDetails extends huma.ErrorModel with the developer-facing
// description and end-user recovery hint sourced from errors/*.yaml.
//
// The embedded ErrorModel keeps the wire payload RFC 9457 compatible
// (type / title / status / detail), while the extra `description` and
// `userAction` fields let the frontend render a richer toast (and the
// SDK surface them as typed properties) without losing backwards
// compatibility with generic problem+json clients, which simply ignore
// unknown members.
//
// Extensions carries optional RFC 9457 extension members keyed by
// `x-` prefixed names. Currently we emit `x-i18n-key` when the
// originating Spec has an i18nKey set in errors/*.yaml, so the
// frontend can prefer a hand-curated translation over the catalog
// default. The field is omitted entirely when no extensions apply,
// keeping the payload byte-identical for the bulk of the catalog
// that has not opted in yet.
type ProblemDetails struct {
	huma.ErrorModel
	Description string         `json:"description,omitempty" doc:"Developer-facing explanation of when this error fires."`
	UserAction  string         `json:"userAction,omitempty" doc:"Short imperative the UI can render to tell the end user how to recover."`
	Extensions  map[string]any `json:"extensions,omitempty" doc:"Optional RFC 9457 extension members. Currently emits x-i18n-key for codes that opt into a curated i18next key."`
}

// GetStatus implements huma.StatusError so Huma sets the response code.
func (p *ProblemDetails) GetStatus() int { return p.Status }

// Error implements the error interface, mirroring huma.ErrorModel's
// formatting so existing log lines remain stable.
func (p *ProblemDetails) Error() string { return p.ErrorModel.Error() }

// HTTPErr converts an apierrors.Spec into a Huma status error so the
// canonical problem+json envelope is emitted by the framework. All
// handler packages should call this instead of defining a local httpErr.
//
// The envelope is RFC 9457-compliant and additionally includes
// description + userAction copied from the error catalog:
//
//   - type:        the machine-readable error code (e.g. "WS.TASK.NOT_FOUND").
//     Clients should branch on this field.
//   - title:       the HTTP status text (e.g. "Not Found"). Populated
//     explicitly for determinism.
//   - detail:      the human-readable message from the Spec. Must NOT
//     be prefixed with the code — clients read `type` for that.
//   - status:      the HTTP status code.
//   - description: developer-facing explanation (omitted when empty).
//   - userAction:  end-user recovery hint (omitted when empty).
func HTTPErr(spec *apierrors.Spec) error {
	return &ProblemDetails{
		ErrorModel: huma.ErrorModel{
			Type:   spec.Code,
			Title:  http.StatusText(spec.Status),
			Status: spec.Status,
			Detail: spec.Message,
		},
		Description: spec.Description,
		UserAction:  spec.UserAction,
		Extensions:  specExtensions(spec),
	}
}

// specExtensions returns the RFC 9457 extension map for a spec. The
// definition lives in [problem] so the Huma-side envelopes built here
// and the raw-writer envelopes emitted by the shared middleware pick up
// a new extension member at the same time.
func specExtensions(spec *apierrors.Spec) map[string]any {
	return problem.Extensions(spec)
}

// HTTPErrFromAPIError converts an *apierr.APIError into the canonical
// problem+json envelope. Use this when a helper returns an APIError
// (carrying detail metadata via [apierr.WithDetail]) and you need to
// surface it with the same shape [HTTPErr] would have produced. The
// detail map is forwarded as RFC 9457 extensions so the SDK can
// surface it for diagnostics.
//
// A nil APIError or one with a nil Spec collapses to
// apierrors.InternalUnexpected so callers can rely on the result
// always implementing huma.StatusError.
func HTTPErrFromAPIError(e *apierr.APIError) error {
	if e == nil || e.Spec == nil {
		return HTTPErr(apierrors.InternalUnexpected)
	}
	ext := specExtensions(e.Spec)
	if len(e.Details) > 0 {
		merged := make(map[string]any, len(e.Details)+len(ext))
		for k, v := range ext {
			merged[k] = v
		}
		for k, v := range e.Details {
			merged[k] = v
		}
		ext = merged
	}
	return &ProblemDetails{
		ErrorModel: huma.ErrorModel{
			Type:   e.Spec.Code,
			Title:  http.StatusText(e.Spec.Status),
			Status: e.Spec.Status,
			Detail: e.Spec.Message,
		},
		Description: e.Spec.Description,
		UserAction:  e.Spec.UserAction,
		Extensions:  ext,
	}
}

// HTTPErrWithRetryAfter is the Retry-After-emitting variant of [HTTPErr].
// Returns a Huma error that additionally implements huma.HeadersError so
// the framework writes a Retry-After response header when retryAfter is
// non-empty. Used by AI provider rate-limit propagation: the upstream
// 429's Retry-After is forwarded so the UI / client can back off the
// same way it would for a direct upstream call.
func HTTPErrWithRetryAfter(spec *apierrors.Spec, retryAfter string) error {
	return &headersProblemDetails{
		ProblemDetails: ProblemDetails{
			ErrorModel: huma.ErrorModel{
				Type:   spec.Code,
				Title:  http.StatusText(spec.Status),
				Status: spec.Status,
				Detail: spec.Message,
			},
			Description: spec.Description,
			UserAction:  spec.UserAction,
			Extensions:  specExtensions(spec),
		},
		retryAfter: retryAfter,
	}
}

// headersProblemDetails extends ProblemDetails with a Retry-After
// response header. Implements huma.HeadersError so Huma writes the
// header before the status code is set.
type headersProblemDetails struct {
	ProblemDetails
	retryAfter string
}

// GetHeaders returns the headers Huma should append to the response.
// Implements huma.HeadersError.
func (p *headersProblemDetails) GetHeaders() http.Header {
	if p == nil || p.retryAfter == "" {
		return nil
	}
	return http.Header{"Retry-After": []string{p.retryAfter}}
}

// WriteSpecError writes a JSON error envelope for raw chi handlers that
// cannot return errors through the Huma pipeline (file downloads,
// streaming responses, etc.). It forwards to [problem.Write], which is
// the same writer the shared authentication and rate-limit middleware
// use, so a client sees one envelope shape no matter which layer
// rejected the request.
func WriteSpecError(w http.ResponseWriter, spec *apierrors.Spec) {
	problem.Write(w, spec)
}

// mysqlErrDuplicateEntry is the MySQL error number for a unique-constraint
// violation (ER_DUP_ENTRY). See https://dev.mysql.com/doc/mysql-errors/8.4/en/
const mysqlErrDuplicateEntry = 1062

// IsDuplicateEntry reports whether the given error is a MySQL duplicate-entry
// error (error 1062 / ER_DUP_ENTRY). It uses a type assertion against the
// mysql driver error type for reliable detection.
func IsDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrDuplicateEntry
}

// ActorPtr returns a pointer to the actor's internal user id (as int64) for
// use in eventbus.Event payloads, or nil when the actor is not present in
// the context.
func ActorPtr(ctx context.Context) *int64 {
	uid, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return nil
	}
	v := int64(uid)
	return &v
}

// PublicIDOrEmpty returns the UUID string representation of a types.PublicID,
// or an empty string when it is the zero value (e.g. a LEFT JOIN returned
// NULL).
func PublicIDOrEmpty(p types.PublicID) string {
	var zero types.PublicID
	if p == zero {
		return ""
	}
	return p.String()
}

// BylineDisplayName renders the display name of the user a row is
// attributed to — the uploader of an attachment, the author of a comment,
// the creator of a lens or widget.
//
// Those users are LEFT JOINed with `enabled = TRUE` in the ON clause, so a
// suspended account yields a NULL name and this returns "". The row stays:
// the file, comment, lens and widget belong to the workspace, not to the
// account that produced them, and suspending someone must not delete their
// contributions out from under the rest of the team. What is withheld is
// only the byline. Clients render their own placeholder for the empty
// string; pair this with [PublicIDOrEmpty] for the matching id, which goes
// empty at the same time.
//
// `enabled = TRUE` may still gate the row itself when the user is what the
// row is about: membership lists, actor and attendee rows, credentials.
func BylineDisplayName(s sql.NullString) string {
	return dbtype.StringFromNullString(s)
}

// NullInt32From wraps a non-null uint32 (typically an internal row ID
// resolved earlier in the handler) into a sql.NullInt32 suitable for
// passing to sqlc-generated query params whose underlying column is
// nullable. The conversion narrows uint32 to int32 — internal ids are
// well below the int32 ceiling in any realistic deployment.
func NullInt32From(v uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(v), Valid: true} //#nosec G115 -- internal row id, bounded by realistic workspace size
}

// Int32ToUint32 unpacks a sql.NullInt32 into the non-null uint32 ID the
// handler layer uses, returning 0 for the NULL case so callers can
// continue treating "no row" as a sentinel without a separate branch.
func Int32ToUint32(n sql.NullInt32) uint32 {
	if !n.Valid {
		return 0
	}
	return uint32(n.Int32) //#nosec G115 -- internal row id, bounded by realistic workspace size; complement of NullInt32From
}

// NullStr converts a sql.NullString to a plain Go string, returning the
// empty string when the column is NULL.
func NullStr(s sql.NullString) string {
	return dbtype.StringFromNullString(s)
}

// NullStrPtr converts a sql.NullString to a *string, returning nil when
// the column is NULL so the field is omitted from JSON.
func NullStrPtr(s sql.NullString) *string {
	return dbtype.PtrFromNullString(s)
}

// NullTime converts a sql.NullTime to a *time.Time, returning nil when the
// column is NULL so the field is omitted from JSON instead of being
// serialised as Go zero-time.
func NullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// NullTimeUnix converts a sql.NullTime to *int64 unix seconds. Returns nil
// for the NULL case so the field is omitted from JSON. This is the single
// conversion point for nullable _at columns, per the api-types convention.
func NullTimeUnix(t sql.NullTime) *int64 {
	return dbtype.UnixSecondsFromNullTime(t)
}

// NullTimeUnixVal converts a sql.NullTime to a unix-seconds int64 value.
// Returns 0 for the NULL case. Use this variant when the DTO field is a
// non-pointer int64 (e.g. updatedAt on rows that always have a value after
// the first mutation).
func NullTimeUnixVal(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

// NullTimeDate converts a sql.NullTime to a *string formatted as YYYY-MM-DD
// in UTC. Returns nil for the NULL case so the field is omitted from JSON.
// This is the single conversion point for nullable _on columns (DATE), per
// the api-types convention.
//
// The .UTC() normalisation matches [NullTimeDateStr]: a sql.NullTime
// carrying a non-UTC location (e.g. JST when the driver applies a session
// timezone) would otherwise format to a different calendar day depending
// on which helper a mapper happened to call. Both helpers must agree.
func NullTimeDate(t sql.NullTime) *string {
	return dbtype.DateStringFromNullTime(t)
}

// NullTimeDateStr is the value-returning twin of [NullTimeDate]. It returns
// the empty string for NULL, suitable for DTOs whose `dueOn` / `startedOn`
// fields are declared as `string` with `omitempty` rather than `*string`.
func NullTimeDateStr(t sql.NullTime) string {
	return dbtype.DateStringValueFromNullTime(t)
}

// BytesToUUIDString converts a raw BINARY(16) public_id column into a
// canonical UUID v7 string. Empty or non-16-byte input returns "".
//
// Use this for non-nullable BINARY(16) columns sqlc returns as []byte.
func BytesToUUIDString(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	var u uuid.UUID
	copy(u[:], b)
	return u.String()
}

// NullBytesToUUIDString converts a sql.NullString whose underlying string
// is a raw BINARY(16) UUID into a canonical hyphenated UUID v7 string.
// Returns "" when NULL or when the value is not exactly 16 bytes.
//
// Use this over [NullStr] for public_id columns: NullStr would assign the
// raw binary bytes straight into the DTO field, leaking the internal
// representation through JSON.
func NullBytesToUUIDString(s sql.NullString) string {
	if !s.Valid || len(s.String) != 16 {
		return ""
	}
	var u uuid.UUID
	copy(u[:], s.String)
	return u.String()
}

// NullBytesToUUIDPtr converts a sql.NullString carrying raw BINARY(16) bytes
// into a UUID string pointer; returns nil when NULL or wrong length, so the
// field is omitted from JSON.
func NullBytesToUUIDPtr(s sql.NullString) *string {
	if !s.Valid || len(s.String) != 16 {
		return nil
	}
	var u uuid.UUID
	copy(u[:], s.String)
	out := u.String()
	return &out
}

// RawBytesToUUIDPtr converts a BINARY(16) column to a UUID string pointer.
// Accepts interface{} because sqlc may expose the column as either []byte
// or interface{} depending on the query (notably joined columns reached
// through SELECT *). Returns nil when the value is not a []byte of exactly
// 16 bytes.
func RawBytesToUUIDPtr(v interface{}) *string {
	b, ok := v.([]byte)
	if !ok || len(b) != 16 {
		return nil
	}
	var u uuid.UUID
	copy(u[:], b)
	out := u.String()
	return &out
}

// FormatUnixISO formats a unix-seconds value as an RFC 3339 timestamp in
// UTC, for CSV exporters.
//
// The API boundary keeps `*_at` as int64 unixtime (see
// docs/conventions/api-types.md) and that does not change. A CSV is a
// different kind of artifact: someone opens it. These columns used to
// carry the raw integer, so a spreadsheet showed ten-digit numbers under
// "Completed At", "Created At" and "Updated At" while "Due Date" and
// "Start Date" — same file, same row — were readable dates.
//
// UTC rather than a viewer's zone because the file has no viewer at the
// time it is written, and a trailing Z says which zone it is rather than
// leaving the reader to guess.
func FormatUnixISO(u int64) string {
	return time.Unix(u, 0).UTC().Format(time.RFC3339)
}

// FormatOptionalUnixISO formats an optional unix-seconds value as
// [FormatUnixISO] does, returning the empty string when nil.
func FormatOptionalUnixISO(u *int64) string {
	if u == nil {
		return ""
	}
	return FormatUnixISO(*u)
}

// DerefStr returns the string value or empty string when the pointer is nil.
// Used by CSV exporters that need to flatten *string DTO fields into
// always-present columns.
func DerefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CheckWorkspaceMember verifies that the given user is an enabled member of
// the workspace. Returns nil on success, sql.ErrNoRows when the user is not
// a member. This is the single canonical existence check — callers no longer
// need to inline the query.
//
// A bearer token bound to a different workspace is reported as ErrNoRows
// too: callers already map that onto their own access-denied spec, so a
// replayed token lands on the 403/404 path instead of the generic 500
// branch reserved for transport failures.
func CheckWorkspaceMember(ctx context.Context, db *sql.DB, workspaceID uint32, userID uint32) error {
	if err := acl.EnforceTokenWorkspace(ctx, workspaceID); err != nil {
		return sql.ErrNoRows
	}
	q := generated.New(db)
	_, err := q.CheckWorkspaceMemberExists(ctx, generated.CheckWorkspaceMemberExistsParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	return err
}

// WorkspaceMemberRole returns the role string ("owner", "admin", "member",
// "guest") for the given user in the workspace. Returns sql.ErrNoRows when
// the user is not an enabled member, or when the request's bearer token is
// bound to a different workspace (see [CheckWorkspaceMember]).
func WorkspaceMemberRole(ctx context.Context, db *sql.DB, workspaceID uint32, userID uint32) (string, error) {
	if err := acl.EnforceTokenWorkspace(ctx, workspaceID); err != nil {
		return "", sql.ErrNoRows
	}
	q := generated.New(db)
	role, err := q.GetWorkspaceMemberRole(ctx, generated.GetWorkspaceMemberRoleParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	return string(role), err
}

// TotalAsInt64 normalises the COUNT(*) OVER() return type into int64.
// MySQL drivers may surface the value as int64, int, uint64 or as a
// decimal byte slice depending on the underlying column type, so all four
// shapes are accepted.
func TotalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		// COUNT(*) OVER() returns a non-negative row count; the only realistic
		// way to overflow int64 here is per-workspace row counts above ~9.2e18,
		// which exceeds the addressable workspace by many orders of magnitude.
		return int64(x) //#nosec G115 -- COUNT(*) result, bounded by workspace size

	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	default:
		return 0
	}
}
