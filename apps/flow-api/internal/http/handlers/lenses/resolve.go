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
// Every consumer of a lens filter goes through this type. The public
// share used to have its own reading — first status value only, priority
// flattened to a min..max range — and the result was a share page that
// answered a different question than the lens its author had saved. A
// priority filter of {1, 4} became "priority between 1 and 4", which put
// tasks the author never selected on a page with no login.
//
// The on-disk JSON has two historically observed shapes and both are
// read here:
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
	// Impossible marks a filter that names something no task can match —
	// a status outside the enum, an unparseable assignee id. Such a
	// filter selects nothing. Ignoring it instead would widen the set,
	// which on a public share page is the direction that leaks.
	Impossible bool
}

// parseLensFilter reads the raw lens_json filter blob into the canonical
// [lensFilter]. Keys the grammar does not define are ignored; values the
// grammar does define but cannot be honoured make the filter impossible
// rather than absent.
func parseLensFilter(raw json.RawMessage) lensFilter {
	out := lensFilter{}
	if len(raw) == 0 {
		return out
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}

	if status, ok := m["status"]; ok {
		var named []string
		switch {
		case status["values"] != nil:
			named = stringsInArray(status["values"])
		case status["in"] != nil:
			named = stringsInArray(status["in"])
		case status["eq"] != nil:
			if s, ok := status["eq"].(string); ok {
				named = []string{s}
			}
		}
		if len(named) > 0 {
			for _, s := range named {
				if _, ok := allowedDerivedStates[s]; ok {
					out.States = append(out.States, s)
				}
			}
			if len(out.States) == 0 {
				// The lens named states, every one of them outside the
				// enum. It selects nothing.
				out.Impossible = true
			}
		}
	}

	if priority, ok := m["priority"]; ok {
		switch {
		case priority["values"] != nil:
			out.Priorities = int32sInArray(priority["values"])
			if len(out.Priorities) == 0 {
				out.Impossible = true
			}
		case priority["in"] != nil:
			out.Priorities = int32sInArray(priority["in"])
			if len(out.Priorities) == 0 {
				out.Impossible = true
			}
		default:
			if v, ok := priority["gte"]; ok {
				if n, ok := numberToInt32(v); ok {
					out.PriorityMin = &n
				}
			}
			if v, ok := priority["gt"]; ok {
				if n, ok := numberToInt32(v); ok {
					n++
					out.PriorityMin = &n
				}
			}
			if v, ok := priority["lte"]; ok {
				if n, ok := numberToInt32(v); ok {
					out.PriorityMax = &n
				}
			}
			if v, ok := priority["lt"]; ok {
				if n, ok := numberToInt32(v); ok {
					n--
					out.PriorityMax = &n
				}
			}
			if v, ok := priority["eq"]; ok {
				if n, ok := numberToInt32(v); ok {
					out.Priorities = []int32{n}
				}
			}
		}
	}

	if due, ok := m["due_on"]; ok {
		if v, ok := due["gte"]; ok {
			if t, ok := parseDateString(v); ok {
				out.DueFrom = &t
			}
		}
		if v, ok := due["lte"]; ok {
			if t, ok := parseDateString(v); ok {
				out.DueTo = &t
			}
		}
		if v, ok := due["eq"]; ok {
			if t, ok := parseDateString(v); ok {
				out.DueFrom = &t
				out.DueTo = &t
			}
		}
	}

	if assignee, ok := m["assignee"]; ok {
		if v, ok := assignee["value"]; ok {
			s, _ := v.(string)
			if s != "" {
				if _, err := types.Parse(s); err != nil {
					out.Impossible = true
				} else {
					out.Assignee = s
				}
			}
		}
	}

	if search, ok := m["search"]; ok {
		if v, ok := search["value"]; ok {
			if s, ok := v.(string); ok {
				out.Search = s
			}
		}
	}

	return out
}

// fragments renders the filter as WHERE fragments over v_task_list
// aliased `v`, plus the matching bind arguments. Every knob the lens
// grammar defines is rendered; there is no "unsupported, therefore
// ignored" branch, because ignoring a filter widens the result and the
// caller here is an unauthenticated share page.
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
// project_id; the filter blob is read by [parseLensFilter] and rendered
// by [lensFilter.fragments], so the shared page selects the same set the
// lens names. Two further predicates are added that the lens itself does
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
	filter, _, _ := parseLensJSON(lens.LensJson)

	where := []string{"v.workspace_id = ?", "v.visibility = 'public'"}
	args := []any{lens.WorkspaceID}
	if lens.ProjectID.Valid {
		where = append(where, "v.project_id = ?")
		args = append(args, lens.ProjectID.Int32)
	}
	lensWhere, lensArgs := parseLensFilter(filter).fragments()
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
