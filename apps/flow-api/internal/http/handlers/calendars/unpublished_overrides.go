package calendars

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// unpublishedOverridesByMaster returns, per published master's public id,
// the occurrences of that series the share page still advertises at the
// time they moved away from.
//
// Publishing a master publishes every occurrence its rule expands to.
// Moving one occurrence writes a separate override row and leaves the rule
// alone, so unless the override is published on the same share the page
// keeps drawing the occurrence at its original start. Nothing on either
// side reports the disagreement, which is what the editor needs told.
//
// One read for the whole listing. The share is named once and the query
// answers for every master it publishes, so the number of statements does
// not grow with the number of events on the page. The masters are keyed by
// public id, which is what the editor rows already carry, so grouping needs
// no second lookup and no internal id reaches this layer.
//
// Confidential overrides are in the result on purpose. One can never be
// published — the attach path refuses it — which makes its occurrence the
// one most worth reporting rather than the one to hide. The visibility
// travels with the row so the editor can tell an override it may offer to
// publish from one the reader has to resolve themselves.
func unpublishedOverridesByMaster(
	ctx context.Context,
	q *calendar.Queries,
	workspaceID uint32,
	sharePID types.PublicID,
) (map[string][]ShareOverrideWarning, error) {
	rows, err := q.ListUnpublishedShareOverrides(ctx, calendar.ListUnpublishedShareOverridesParams{
		WorkspaceID: workspaceID,
		PublicID:    sharePID,
	})
	if err != nil {
		return nil, err
	}

	var out map[string][]ShareOverrideWarning
	for _, row := range rows {
		// The query already refuses a row missing either instant; describing
		// the discrepancy needs both, and a zero would read as 1970.
		if !row.RecurrenceOriginalStart.Valid || !row.StartAt.Valid {
			continue
		}
		if out == nil {
			out = make(map[string][]ShareOverrideWarning)
		}
		master := row.MasterPublicID.String()
		out[master] = append(out[master], ShareOverrideWarning{
			OriginalStart: nullTimeUnixValue(row.RecurrenceOriginalStart),
			EventID:       row.EventPublicID.String(),
			StartAt:       nullTimeUnixValue(row.StartAt),
			Title:         row.Title,
			Visibility:    string(row.Visibility),
		})
	}
	return out, nil
}
