package lenses

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// publicLensTaskCap is the hard upper bound on the number of tasks
// returned by the unauthenticated GET /public/lenses/{token} endpoint.
// Public shares are not paginated; if the underlying query produces
// more than this many rows, the cap silently truncates the tail. Kept
// at 200 because the share page renders a single non-virtualised table
// and shouldn't be allowed to OOM the browser or leak the entire
// workspace as a free data dump.
const publicLensTaskCap int32 = 200

// publicLensFilter is the parsed view of the lens_json `filter` map that
// the resolver actually consumes. The on-disk JSON has two historically
// observed shapes:
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
//
// The resolver only honours filters that the public-task SQL query
// supports server-side (status, priority range, due range). Unsupported
// knobs (search, assignee, label, blocked, ...) are ignored — the share
// page is intentionally a coarse-grained projection, not a full task
// list.
type publicLensFilter struct {
	// State, if non-empty, restricts the result set to a single
	// derived_state. Multi-state filters are not collapsed; the share
	// query only takes a scalar.
	State string
	// PriorityMin / PriorityMax bracket the priority column. Both are
	// optional; nil means "no bound on this side".
	PriorityMin *int32
	PriorityMax *int32
	// DueFrom / DueTo bracket due_on. Both are optional.
	DueFrom *time.Time
	DueTo   *time.Time
}

// parsePublicLensFilter projects the raw lens_json filter blob into the
// subset of knobs the public resolver supports. Unknown / unsupported
// fields are ignored silently rather than rejecting the lens because
// public shares should degrade gracefully when faced with filters the
// public path cannot honour.
func parsePublicLensFilter(raw json.RawMessage) publicLensFilter {
	out := publicLensFilter{}
	if len(raw) == 0 {
		return out
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}

	if status, ok := m["status"]; ok {
		// Both shapes: prefer "values" then "in" then "eq". Any other
		// shape (e.g. {"neq": ...}) is ignored.
		if v, ok := status["values"]; ok {
			out.State = firstStringInArray(v)
		} else if v, ok := status["in"]; ok {
			out.State = firstStringInArray(v)
		} else if v, ok := status["eq"]; ok {
			if s, ok := v.(string); ok {
				out.State = s
			}
		}
	}

	if priority, ok := m["priority"]; ok {
		if v, ok := priority["values"]; ok {
			lo, hi := minMaxInt32InArray(v)
			out.PriorityMin = lo
			out.PriorityMax = hi
		} else if v, ok := priority["in"]; ok {
			lo, hi := minMaxInt32InArray(v)
			out.PriorityMin = lo
			out.PriorityMax = hi
		} else {
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
					out.PriorityMin = &n
					out.PriorityMax = &n
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

	return out
}

// firstStringInArray returns the first string element of v when v is a
// JSON array, or "" otherwise. Used to reduce a multi-value status
// filter down to the single value the public query supports.
func firstStringInArray(v any) string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	if s, ok := arr[0].(string); ok {
		return s
	}
	return ""
}

// minMaxInt32InArray returns the smallest and largest int32 in v when v
// is a JSON array of numbers. Either may be nil if the array is empty
// or contains no numeric elements.
func minMaxInt32InArray(v any) (*int32, *int32) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil, nil
	}
	var lo, hi int32
	seen := false
	for _, x := range arr {
		n, ok := numberToInt32(x)
		if !ok {
			continue
		}
		if !seen || n < lo {
			lo = n
		}
		if !seen || n > hi {
			hi = n
		}
		seen = true
	}
	if !seen {
		return nil, nil
	}
	return &lo, &hi
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

// resolvePublicLensTasks runs the public-safe task projection for a
// shared lens. The lens row carries the workspace_id and (optional)
// project_id; the filter blob is parsed via parsePublicLensFilter and
// passed through to the ListPublicLensTasks sqlc query. The result is
// hard-capped at publicLensTaskCap rows.
//
// Errors propagate to the caller as opaque internals; the
// FindLensByPublicToken call already validated the token, so a failure
// here means the database is unhappy.
func resolvePublicLensTasks(
	ctx context.Context,
	q *generated.Queries,
	lens generated.FindLensByPublicTokenRow,
) ([]PublicLensTask, error) {
	filter, _, _ := parseLensJSON(lens.LensJson)
	parsed := parsePublicLensFilter(filter)

	params := generated.ListPublicLensTasksParams{
		WorkspaceID: lens.WorkspaceID,
		ProjectID:   lens.ProjectID,
		StateFilter: generated.TasksDerivedState(parsed.State),
		PriorityMin: nullInt32FromPtr(parsed.PriorityMin),
		PriorityMax: nullInt32FromPtr(parsed.PriorityMax),
		DueFrom:     nullTimeFromPtr(parsed.DueFrom),
		DueTo:       nullTimeFromPtr(parsed.DueTo),
		Limit:       publicLensTaskCap,
	}

	rows, err := q.ListPublicLensTasks(ctx, params)
	if err != nil {
		return nil, err
	}

	out := make([]PublicLensTask, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToPublicLensTask(r))
	}
	return out, nil
}

// nullInt32FromPtr converts a *int32 into a sql.NullInt32; nil maps to
// {Valid: false}.
func nullInt32FromPtr(p *int32) sql.NullInt32 {
	if p == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *p, Valid: true}
}

// nullTimeFromPtr converts a *time.Time into a sql.NullTime; nil maps
// to {Valid: false}.
func nullTimeFromPtr(p *time.Time) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *p, Valid: true}
}
