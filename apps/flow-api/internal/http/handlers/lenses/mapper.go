package lenses

import (
	"encoding/json"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// lensJSON is the internal structure stored in the lens_json column.
type lensJSON struct {
	Filter  json.RawMessage `json:"filter"`
	Sort    json.RawMessage `json:"sort"`
	GroupBy *string         `json:"groupBy"`
}

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

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

// nullTimeUnix delegates to handlerutil.NullTimeUnix (returns *int64, nil for NULL).
var nullTimeUnix = handlerutil.NullTimeUnix

// nullString delegates to handlerutil.NullStrPtr.
var nullString = handlerutil.NullStrPtr
