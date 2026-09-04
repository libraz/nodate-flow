package lenses

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// publicLensTaskCap is the hard upper bound on the number of tasks
// returned by the unauthenticated GET /public/lenses/{token} endpoint.
// Public shares are not paginated; if the underlying query produces
// more than this many rows, the cap silently truncates the tail. Kept
// at 200 because the share page renders a single non-virtualised table
// and shouldn't be allowed to OOM the browser or leak the entire
// workspace as a free data dump.
const publicLensTaskCap int32 = 200

// allowedDerivedStates gate-keeps the status values a lens may name, so
// a filter carrying something outside the enum cannot be dropped on the
// floor and silently widen the shared set.
var allowedDerivedStates = map[string]struct{}{
	"open":      {},
	"waiting":   {},
	"review":    {},
	"done":      {},
	"cancelled": {},
}

// lensFilter is the canonical reading of the lens_json `filter` map: the
// set of tasks the lens names, expressed the way the lens grammar
// expresses it.
//
// Every consumer of a lens filter goes through this type, so the share
// page answers the same question as the lens its author saved. The
// on-disk JSON has two shapes and both are read here:
//
//  1. UI-emitted shape (lens-picker.tsx -> taskFiltersToLensFilter):
//     { "status":   {"values":[...]},
//     "priority": {"values":[...]},
//     "assignee": {"value":"..."},
//     "search":   {"value":"..."} }
//
//  2. NL-query / closed grammar shape (nlquery.go):
//     { "status":   {"in":[...]} | {"eq":"open"},
//     "priority": {"gte":3} | {"in":[3,4]},
//     "due_on":   {"between":"this_week"} | {"gte":"YYYY-MM-DD","lte":"YYYY-MM-DD"} }
type lensFilter struct {
	// States is the set of derived_state values the lens names. Empty
	// means the lens does not filter on state.
	States []string
	// Priorities is the set of priority values the lens names. Empty
	// means no set was given; PriorityMin / PriorityMax may still bound
	// the column when the filter used a comparison instead.
	Priorities []int32
	// PriorityMin / PriorityMax bracket the priority column for the
	// comparison shapes (gte / gt / lte / lt). Both optional.
	PriorityMin *int32
	PriorityMax *int32
	// DueFrom / DueTo bracket due_on. Both optional.
	DueFrom *time.Time
	DueTo   *time.Time
	// Assignee is the primary-assignee public id the lens names, empty
	// when unfiltered.
	Assignee string
	// Search is the case-insensitive title substring the lens names,
	// empty when unfiltered.
	Search string
	// Impossible marks a filter that selects nothing: one naming
	// something no task can match (a status outside the enum, an
	// unparseable assignee id), and one this reader cannot render in
	// full. Rendering less than the whole filter would widen the set,
	// which on a public share page is the direction that leaks.
	Impossible bool
}

// readableFilterKeys maps each filter key this reader renders to the
// operators it renders for that key.
//
// It is deliberately narrower than the lens grammar in
// internal/ai/nlquery: keys and operators the grammar defines but this
// reader does not render are absent, as is anything the grammar does not
// define at all. Absence is what makes a filter impossible, so the set
// below is the single place that decides what a share page can express.
var readableFilterKeys = map[string]map[string]struct{}{
	"status":   {"values": {}, "in": {}, "eq": {}},
	"priority": {"values": {}, "in": {}, "eq": {}, "gt": {}, "gte": {}, "lt": {}, "lte": {}},
	"due_on":   {"gte": {}, "lte": {}, "eq": {}},
	"assignee": {"value": {}},
	"search":   {"value": {}},
}

// readLensFilter is the one reading of a lens filter blob. It returns
// the canonical [lensFilter] and, when the blob cannot be rendered in
// full, the JSON path of what stopped it — "filter" for a blob that does
// not decode, "filter.<key>" for a key this reader does not render or
// whose values it cannot honour, "filter.<key>.<op>" for an operator it
// does not render. An empty path means the whole blob was read.
//
// Both callers go through here, so what a lens may be written in and
// what a share page can render are one description rather than two that
// drift: [validateLensFilter] refuses a write it names a path for, and
// [parseLensFilter] treats the same path as a filter that selects
// nothing.
//
// A blob is read all-or-nothing. Naming a key or operator outside
// [readableFilterKeys], combining operators the renderer would apply only
// some of, or carrying a value that cannot be honoured stops the whole
// reading, because rendering the remainder drops a predicate and a
// dropped predicate widens the set.
//
// An empty blob is not a partial reading. It means the lens names every
// task in its own scope, and the resolver's workspace and visibility
// predicates still apply.
func readLensFilter(raw json.RawMessage) (lensFilter, string) {
	out := lensFilter{}
	if len(raw) == 0 {
		return out, ""
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return out, "filter"
	}

	for key, ops := range m {
		readable, ok := readableFilterKeys[key]
		if !ok || len(ops) == 0 {
			return out, "filter." + key
		}
		for op := range ops {
			if _, ok := readable[op]; !ok {
				return out, "filter." + key + "." + op
			}
		}
		var read bool
		switch key {
		case "status":
			read = out.readStatus(ops)
		case "priority":
			read = out.readPriority(ops)
		case "due_on":
			read = out.readDueOn(ops)
		case "assignee":
			read = out.readAssignee(ops)
		case "search":
			read = out.readSearch(ops)
		}
		if !read {
			return out, "filter." + key
		}
	}

	return out, ""
}

// parseLensFilter reads a stored filter blob for the share resolver. A
// blob [readLensFilter] could not read in full makes the filter
// impossible, so it selects nothing.
//
// Write-time validation refuses such a blob, so reaching this branch
// means a row that predates the refusal or one written around it. An
// absent filter is the one thing the reading must never become: the
// caller is an unauthenticated share page, and a lens with no filter
// publishes every public task in its scope.
func parseLensFilter(raw json.RawMessage) lensFilter {
	filter, unread := readLensFilter(raw)
	if unread != "" {
		return lensFilter{Impossible: true}
	}
	return filter
}

// validateLensFilter refuses a filter blob the share resolver could not
// render in full, naming the path that stopped the reading. The lens
// endpoints call it before storing, so a filter that would publish a set
// nobody named is a rejected write rather than a row the resolver has to
// defend against.
func validateLensFilter(raw json.RawMessage) error {
	if _, unread := readLensFilter(raw); unread != "" {
		return handlerutil.HTTPErrFromAPIError(
			apierr.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", unread))
	}
	return nil
}

// readStatus reads the status key into States. Exactly one operator may
// be present: the renderer emits a single membership test, so a second
// operator would go unrendered. Named states outside the enum are
// dropped — no task carries them, so they cannot narrow the set — but a
// filter left with no state at all names nothing this reader can honour.
func (f *lensFilter) readStatus(ops map[string]any) bool {
	if len(ops) != 1 {
		return false
	}
	var named []string
	switch {
	case ops["values"] != nil:
		named = stringsInArray(ops["values"])
	case ops["in"] != nil:
		named = stringsInArray(ops["in"])
	case ops["eq"] != nil:
		s, ok := ops["eq"].(string)
		if !ok {
			return false
		}
		named = []string{s}
	}
	if len(named) == 0 {
		return false
	}
	for _, s := range named {
		if _, ok := allowedDerivedStates[s]; ok {
			f.States = append(f.States, s)
		}
	}
	return len(f.States) > 0
}

// readPriority reads the priority key. The set forms (values / in / eq)
// and the comparison forms (gt / gte / lt / lte) are rendered by
// different predicates and the renderer emits one reading of the column,
// so a filter mixing them, or naming the same bound twice, is not
// rendered in full.
func (f *lensFilter) readPriority(ops map[string]any) bool {
	_, hasValues := ops["values"]
	_, hasIn := ops["in"]
	_, hasEq := ops["eq"]
	if hasValues || hasIn || hasEq {
		if len(ops) != 1 {
			return false
		}
		switch {
		case hasValues:
			f.Priorities = int32sInArray(ops["values"])
		case hasIn:
			f.Priorities = int32sInArray(ops["in"])
		default:
			n, ok := numberToInt32(ops["eq"])
			if !ok {
				return false
			}
			f.Priorities = []int32{n}
		}
		return len(f.Priorities) > 0
	}

	for op, v := range ops {
		n, ok := numberToInt32(v)
		if !ok {
			return false
		}
		switch op {
		case "gte", "gt":
			if f.PriorityMin != nil {
				return false
			}
			if op == "gt" {
				n++
			}
			f.PriorityMin = &n
		case "lte", "lt":
			if f.PriorityMax != nil {
				return false
			}
			if op == "lt" {
				n--
			}
			f.PriorityMax = &n
		default:
			return false
		}
	}
	return true
}

// readDueOn reads the due_on key into the DueFrom / DueTo bracket. eq
// pins both ends and stands alone; gte and lte may appear together.
func (f *lensFilter) readDueOn(ops map[string]any) bool {
	if v, ok := ops["eq"]; ok {
		if len(ops) != 1 {
			return false
		}
		t, ok := parseDateString(v)
		if !ok {
			return false
		}
		f.DueFrom, f.DueTo = &t, &t
		return true
	}
	for op, v := range ops {
		t, ok := parseDateString(v)
		if !ok {
			return false
		}
		switch op {
		case "gte":
			f.DueFrom = &t
		case "lte":
			f.DueTo = &t
		default:
			return false
		}
	}
	return true
}

// readAssignee reads the assignee key. The value has to be a public id
// the resolver can bind; anything else names nobody.
func (f *lensFilter) readAssignee(ops map[string]any) bool {
	s, ok := ops["value"].(string)
	if !ok || s == "" {
		return false
	}
	if _, err := types.Parse(s); err != nil {
		return false
	}
	f.Assignee = s
	return true
}

// readSearch reads the search key. An empty needle constrains nothing,
// which is what a substring match on it would do anyway.
func (f *lensFilter) readSearch(ops map[string]any) bool {
	s, ok := ops["value"].(string)
	if !ok {
		return false
	}
	f.Search = s
	return true
}

// fragments renders the filter as WHERE fragments over v_task_list
// aliased `v`, plus the matching bind arguments. Every knob [lensFilter]
// carries is rendered; there is no "unsupported, therefore ignored"
// branch, because ignoring a knob widens the result and the caller here
// is an unauthenticated share page. Anything the reader could not turn
// into a knob has already made the filter impossible, which renders as
// the predicate that excludes everything.
func (f lensFilter) fragments() ([]string, []any) {
	var (
		where []string
		args  []any
	)
	if f.Impossible {
		return []string{"1 = 0"}, nil
	}

	if len(f.States) > 0 {
		where = append(where, "v.derived_state IN ("+placeholders(len(f.States))+")")
		for _, s := range f.States {
			args = append(args, s)
		}
	}
	if len(f.Priorities) > 0 {
		where = append(where, "v.priority IN ("+placeholders(len(f.Priorities))+")")
		for _, p := range f.Priorities {
			args = append(args, p)
		}
	}
	if f.PriorityMin != nil {
		where = append(where, "v.priority >= ?")
		args = append(args, *f.PriorityMin)
	}
	if f.PriorityMax != nil {
		where = append(where, "v.priority <= ?")
		args = append(args, *f.PriorityMax)
	}
	if f.DueFrom != nil {
		where = append(where, "(v.due_on IS NOT NULL AND v.due_on >= ?)")
		args = append(args, *f.DueFrom)
	}
	if f.DueTo != nil {
		where = append(where, "(v.due_on IS NOT NULL AND v.due_on <= ?)")
		args = append(args, *f.DueTo)
	}
	if f.Assignee != "" {
		// Matches the assignee semantics of the task list: any enabled
		// assignee actor, not just the primary one the share page shows.
		where = append(where, `EXISTS (
      SELECT 1 FROM task_actors ta
      INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
      INNER JOIN tasks tk ON tk.public_id = v.public_id AND tk.enabled = TRUE
      WHERE ta.task_id = tk.id
        AND ta.enabled = TRUE
        AND ta.role = 'assignee'
        AND u.public_id = ?
    )`)
		pub, _ := types.Parse(f.Assignee)
		args = append(args, pub)
	}
	if f.Search != "" {
		where = append(where, "LOWER(v.title) LIKE ?")
		args = append(args, "%"+stringutil.EscapeLike(strings.ToLower(f.Search))+"%")
	}
	return where, args
}

// placeholders renders n comma-separated bind placeholders.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// stringsInArray returns every string element of v when v is a JSON
// array, in order.
func stringsInArray(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// int32sInArray returns every numeric element of v narrowed to int32,
// when v is a JSON array.
func int32sInArray(v any) []int32 {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int32, 0, len(arr))
	for _, x := range arr {
		if n, ok := numberToInt32(x); ok {
			out = append(out, n)
		}
	}
	return out
}

// numberToInt32 narrows a JSON-decoded number (always float64) into an
// int32. Returns false if v is not a number.
func numberToInt32(v any) (int32, bool) {
	switch n := v.(type) {
	case float64:
		return int32(n), true
	case int:
		return int32(n), true //#nosec G115 -- bounded by lens grammar (priority 0..4)
	case int32:
		return n, true
	case int64:
		return int32(n), true //#nosec G115 -- bounded by lens grammar (priority 0..4)
	default:
		return 0, false
	}
}

// parseDateString attempts to parse v as a YYYY-MM-DD calendar date in
// UTC. Returns false if v is not a string or does not match the layout.
func parseDateString(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// publicLensFilter reads a stored lens_json blob down to the filter the
// share page renders. A blob whose own envelope cannot be read is an
// impossible filter rather than an absent one, for the same reason
// [parseLensFilter] treats an unreadable filter that way: the share URL
// carries no reader identity, so an unreadable lens can only be answered
// with nothing.
func publicLensFilter(lensJSON json.RawMessage) lensFilter {
	var lj struct {
		Filter json.RawMessage `json:"filter"`
	}
	if err := json.Unmarshal(lensJSON, &lj); err != nil {
		return lensFilter{Impossible: true}
	}
	return parseLensFilter(lj.Filter)
}

// publicLensTaskColumns is the public-safe projection the share page
// renders: no internal ids, no description, no creator, no workspace
// metadata.
const publicLensTaskColumns = `v.public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  u.display_name AS assignee_display_name`

// resolvePublicLensTasks runs the public-safe task projection for a
// shared lens. The lens row carries the workspace_id and (optional)
// project_id; the filter blob is read by [publicLensFilter] and rendered
// by [lensFilter.fragments], so the shared page selects the same set the
// lens names, and nothing at all when that set cannot be determined. Two further predicates are added that the lens itself does
// not carry — the workspace scope and `visibility = 'public'` — because
// a share URL has no reader identity to check anything else against.
// The result is hard-capped at publicLensTaskCap rows.
//
// Errors propagate to the caller as opaque internals; the
// FindLensByPublicTokenHash call already validated the token, so a failure
// here means the database is unhappy.
func resolvePublicLensTasks(
	ctx context.Context,
	db *sql.DB,
	lens generated.FindLensByPublicTokenHashRow,
) ([]PublicLensTask, error) {
	where := []string{"v.workspace_id = ?", "v.visibility = 'public'"}
	args := []any{lens.WorkspaceID}
	if lens.ProjectID.Valid {
		where = append(where, "v.project_id = ?")
		args = append(args, lens.ProjectID.Int32)
	}
	lensWhere, lensArgs := publicLensFilter(lens.LensJson).fragments()
	where = append(where, lensWhere...)
	args = append(args, lensArgs...)

	//#nosec G201 -- WHERE fragments are static literals composed in this file; all lens-supplied values are bound via placeholders.
	q := fmt.Sprintf(`SELECT
  %s
FROM v_task_list v
LEFT JOIN users u
  ON u.public_id = v.primary_assignee_public_id AND u.enabled = TRUE
WHERE %s
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ?`, publicLensTaskColumns, strings.Join(where, " AND "))
	args = append(args, publicLensTaskCap)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PublicLensTask, 0, publicLensTaskCap)
	for rows.Next() {
		// The projection matches ListPublicLensTasksRow field for field
		// so the existing mapper stays the one place a row becomes a DTO.
		var r generated.ListPublicLensTasksRow
		if err := rows.Scan(
			&r.PublicID,
			&r.Title,
			&r.DerivedState,
			&r.Priority,
			&r.DueOn,
			&r.AssigneeDisplayName,
		); err != nil {
			return nil, err
		}
		out = append(out, rowToPublicLensTask(r))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
