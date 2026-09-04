package lenses

import (
	"database/sql"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// optionalNullString converts a *string from a request body into a
// sql.NullString. nil and empty string both decode to {Valid: false}
// because the column is nullable and empty descriptions add no value.
func optionalNullString(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

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
		CreatorID:          publicIDOrEmpty(r.CreatorPublicID),
		CreatorDisplayName: bylineDisplayName(r.CreatorDisplayName),
		Name:               r.Name,
		Description:        nullString(r.Description),
		Filter:             filter,
		Sort:               sort,
		GroupBy:            groupBy,
		IsDefault:          r.IsDefault,
		IsPublic:           r.IsPublic,
		SharedAt:           nullTimeUnix(r.SharedAt),
		SafetyCheckedAt:    nullTimeUnix(r.SafetyCheckedAt),
		SortWeight:         r.SortWeight,
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// rowToLensFromGet maps a GetLensByPublicIDRow to the SavedLens DTO.
func rowToLensFromGet(r generated.GetLensByPublicIDRow) SavedLens {
	filter, sort, groupBy := parseLensJSON(r.LensJson)
	return SavedLens{
		ID:                 r.PublicID.String(),
		CreatorID:          publicIDOrEmpty(r.CreatorPublicID),
		CreatorDisplayName: bylineDisplayName(r.CreatorDisplayName),
		Name:               r.Name,
		Description:        nullString(r.Description),
		Filter:             filter,
		Sort:               sort,
		GroupBy:            groupBy,
		IsDefault:          r.IsDefault,
		IsPublic:           r.IsPublic,
		SharedAt:           nullTimeUnix(r.SharedAt),
		SafetyCheckedAt:    nullTimeUnix(r.SafetyCheckedAt),
		SortWeight:         r.SortWeight,
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// rowToPublicLens maps a FindLensByPublicTokenHashRow to the PublicLens DTO.
// Only exposes what the share page renders: the lens heading and, once
// the caller has run the resolver, the tasks. Workspace and creator
// metadata are omitted, and so is the stored lens definition — see
// PublicLens for why the reader of an unauthenticated link is not shown
// the query. The Tasks slice is initialised to a non-nil empty slice so
// the JSON encoder emits `[]` rather than `null`.
func rowToPublicLens(r generated.FindLensByPublicTokenHashRow) PublicLens {
	return PublicLens{
		ID:          r.PublicID.String(),
		Name:        r.Name,
		Description: nullString(r.Description),
		Tasks:       []PublicLensTask{},
	}
}

// rowToPublicLensTask maps a ListPublicLensTasksRow to the PublicLensTask
// DTO. due_on is rendered as a YYYY-MM-DD string per the API time
// convention. No person is carried across — see PublicLensTask.
func rowToPublicLensTask(r generated.ListPublicLensTasksRow) PublicLensTask {
	return PublicLensTask{
		ID:       r.PublicID.String(),
		Title:    r.Title,
		Status:   string(r.DerivedState),
		Priority: r.Priority,
		DueOn:    nullDateString(r.DueOn),
	}
}

// nullTimeUnix delegates to handlerutil.NullTimeUnix (returns *int64, nil for NULL).
var nullTimeUnix = handlerutil.NullTimeUnix

// bylineDisplayName and publicIDOrEmpty delegate to handlerutil. A lens is a
// shared saved view, so a suspended creator empties the byline rather than
// dropping the lens out of everyone else's list.
var (
	bylineDisplayName = handlerutil.BylineDisplayName
	publicIDOrEmpty   = handlerutil.PublicIDOrEmpty
)

// nullString delegates to handlerutil.NullStrPtr.
var nullString = handlerutil.NullStrPtr

// nullDateString delegates to handlerutil.NullTimeDate (returns *string
// formatted as YYYY-MM-DD, nil for NULL).
var nullDateString = handlerutil.NullTimeDate
