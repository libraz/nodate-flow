package lenses

import (
	"database/sql"
	"encoding/json"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// lensJSON is the internal structure stored in the lens_json column.
type lensJSON struct {
	Filter  json.RawMessage `json:"filter"`
	Sort    json.RawMessage `json:"sort"`
	GroupBy *string         `json:"groupBy"`
}

// totalAsInt64 normalizes the COUNT(*) OVER() return type into int64.
func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
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

// buildLensJSON serializes filter, sort, and groupBy into the single
// JSON blob stored in the lens_json column.
func buildLensJSON(filter, sort json.RawMessage, groupBy *string) json.RawMessage {
	lj := lensJSON{
		Filter:  filter,
		Sort:    sort,
		GroupBy: groupBy,
	}
	b, _ := json.Marshal(lj)
	return b
}

// parseLensJSON unpacks the lens_json column into its component fields.
func parseLensJSON(raw json.RawMessage) (filter, sort json.RawMessage, groupBy *string) {
	var lj lensJSON
	if err := json.Unmarshal(raw, &lj); err != nil {
		return json.RawMessage("{}"), json.RawMessage("[]"), nil
	}
	if lj.Filter == nil {
		lj.Filter = json.RawMessage("{}")
	}
	if lj.Sort == nil {
		lj.Sort = json.RawMessage("[]")
	}
	return lj.Filter, lj.Sort, lj.GroupBy
}

// rowToLensFromList maps a ListLensesForProjectRow to the SavedLens DTO.
func rowToLensFromList(r generated.ListLensesForProjectRow) SavedLens {
	filter, sort, groupBy := parseLensJSON(r.LensJson)
	return SavedLens{
		ID:                 r.PublicID.String(),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		Name:               r.Name,
		Filter:             filter,
		Sort:               sort,
		GroupBy:            groupBy,
		IsDefault:          r.IsDefault,
		IsPublic:           r.IsPublic,
		PublicToken:        nullString(r.PublicToken),
		SharedAt:           nullTimeUnix(r.SharedAt),
		SafetyCheckedAt:    nullTimeUnix(r.SafetyCheckedAt),
		SortWeight:         r.SortWeight,
		UpdatedAt:          nullTime(r.UpdatedAt),
		CreatedAt:          r.CreatedAt,
	}
}

// rowToLensFromGet maps a GetLensByPublicIDRow to the SavedLens DTO.
func rowToLensFromGet(r generated.GetLensByPublicIDRow) SavedLens {
	filter, sort, groupBy := parseLensJSON(r.LensJson)
	return SavedLens{
		ID:                 r.PublicID.String(),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		Name:               r.Name,
		Filter:             filter,
		Sort:               sort,
		GroupBy:            groupBy,
		IsDefault:          r.IsDefault,
		IsPublic:           r.IsPublic,
		PublicToken:        nullString(r.PublicToken),
		SharedAt:           nullTimeUnix(r.SharedAt),
		SafetyCheckedAt:    nullTimeUnix(r.SafetyCheckedAt),
		SortWeight:         r.SortWeight,
		UpdatedAt:          nullTime(r.UpdatedAt),
		CreatedAt:          r.CreatedAt,
	}
}

// rowToPublicLens maps a FindLensByPublicTokenRow to the PublicLens DTO.
// Only exposes the lens definition; all workspace/creator metadata is omitted.
func rowToPublicLens(r generated.FindLensByPublicTokenRow) PublicLens {
	filter, sort, groupBy := parseLensJSON(r.LensJson)
	return PublicLens{
		ID:      r.PublicID.String(),
		Name:    r.Name,
		Filter:  filter,
		Sort:    sort,
		GroupBy: groupBy,
	}
}

// nullTimeUnix converts a sql.NullTime to *int64 unix seconds. This is
// the single conversion point for _at columns in this package, per the
// api-types convention.
func nullTimeUnix(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Time.Unix()
	return &v
}

// nullString converts a sql.NullString to *string.
func nullString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}
