package handlerutil_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"

	aitypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/ai"
	audittypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/audit"
	dashboardtypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/dashboard"
	exporttypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/export"
	favoritestypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/favorites"
	importstypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/imports"
	inboxtypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/inbox"
	intaketypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/intake"
	labelstypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/labels"
	lensestypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/lenses"
	notificationstypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/notifications"
	pagestypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/pages"
	projectstypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/projects"
	relationstypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/relations"
	taskstypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/tasks"
	timeboxestypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/timeboxes"
	timelinetypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/timeline"
	webhookstypes "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/webhooks"
)

// limitTagSpec captures the parsed `query:"limit"` constraints from a
// handler input struct.
type limitTagSpec struct {
	owner   string
	def     int
	min     int
	max     int
	present bool
}

// TestPaginationDoesNotDriftAboveCanonicalCap walks a representative
// sample of handler Input structs exposing a `query:"limit"` field and
// asserts:
//
//   - the dominant default is handlerutil.DefaultListLimit (50)
//   - the dominant max is handlerutil.MaxListLimit (200)
//   - any deviation stays within the audit envelope (default <= 100,
//     max <= 1000) so a future PR cannot quietly raise the cap to
//     5000-10000 without an explicit allow-list entry below
//
// The allow-list is the single point that documents *why* an endpoint
// is allowed to deviate. New entries require a reviewer to confirm
// the policy in CLAUDE.md / requirements §13.
func TestPaginationDoesNotDriftAboveCanonicalCap(t *testing.T) {
	t.Parallel()

	// Inputs that legitimately exceed handlerutil.MaxListLimit. Each
	// entry carries the absolute caps the audit policy permits; the
	// scanner treats anything outside the envelope as a regression.
	allowed := map[string]struct {
		maxDefault int
		maxMax     int
	}{
		// Bulk task export — hard cap enforced in handler at maxExportRows=10000.
		"export.Input": {maxDefault: 5000, maxMax: 10000},
		// Cross-workspace dashboard surfaces — see types.go comments.
		"tasks.ListMyTasksInput":          {maxDefault: 100, maxMax: 1000},
		"tasks.ListMyTasksWithDatesInput": {maxDefault: 100, maxMax: 1000},
		// Umbrella link graph fan-in / fan-out.
		"tasks.ListLinkedEventsInput": {maxDefault: 100, maxMax: 1000},
		"tasks.ListLinkedTasksInput":  {maxDefault: 100, maxMax: 1000},
		// Bounded-population per-task rosters: kept at MaxListLimit but
		// with default 100 so the UI gets the full roster in one round-trip.
		"tasks.ListTaskActorsInput":      {maxDefault: 100, maxMax: int(handlerutil.MaxListLimit)},
		"tasks.ListTaskAgentActorsInput": {maxDefault: 100, maxMax: int(handlerutil.MaxListLimit)},
		// AI invocations: cost-bounded surface, tighter than canonical.
		"tasks.ListTaskAiInvocationsInput": {maxDefault: 50, maxMax: int(handlerutil.MaxListLimit)},
	}

	// Sample inputs across every handler package to exercise the audit
	// in one place. Adding a new list endpoint should add a sample
	// here; otherwise the regression guard does not see it.
	samples := []any{
		aitypes.ListInvocationsInput{},
		audittypes.ListAuditLogsInput{},
		dashboardtypes.ListWidgetsInput{},
		exporttypes.Input{},
		favoritestypes.ListFavoritesInput{},
		importstypes.ListImportsInput{},
		inboxtypes.ListInboxInput{},
		intaketypes.ListIntakeItemsInput{},
		labelstypes.ListLabelsInput{},
		lensestypes.ListLensesInput{},
		notificationstypes.ListInput{},
		pagestypes.ListPagesInput{},
		projectstypes.ListProjectsInput{},
		relationstypes.ListForWorkspaceInput{},
		relationstypes.ListForTaskInput{},
		taskstypes.ListMyTasksInput{},
		taskstypes.ListMyTasksWithDatesInput{},
		taskstypes.ListLinkedEventsInput{},
		taskstypes.ListLinkedTasksInput{},
		taskstypes.ListTaskActorsInput{},
		taskstypes.ListTaskAgentActorsInput{},
		taskstypes.ListTaskAiInvocationsInput{},
		taskstypes.ListTaskCommentsInput{},
		taskstypes.ListTaskAttachmentsInput{},
		taskstypes.ListArchivedTasksInput{},
		taskstypes.ListTasksInput{},
		timeboxestypes.ListTimeboxesInput{},
		timelinetypes.ListTimelineForWorkspaceInput{},
		webhookstypes.ListInput{},
		webhookstypes.ListDeliveriesInput{},
	}

	canonicalCount := 0
	for _, s := range samples {
		spec := extractLimitTag(s)
		if !spec.present {
			continue
		}
		isCanonical := spec.def == int(handlerutil.DefaultListLimit) &&
			spec.max == int(handlerutil.MaxListLimit) &&
			spec.min == 1
		if isCanonical {
			canonicalCount++
			continue
		}
		policy, ok := allowed[spec.owner]
		if !ok {
			t.Errorf("%s deviates from canonical pagination (default=%d max=%d) but is not in the allow-list. "+
				"Add it to TestPaginationDoesNotDriftAboveCanonicalCap with a documented policy, or align with handlerutil.{DefaultListLimit,MaxListLimit}.",
				spec.owner, spec.def, spec.max)
			continue
		}
		if spec.def > policy.maxDefault {
			t.Errorf("%s default %d exceeds allow-list ceiling %d", spec.owner, spec.def, policy.maxDefault)
		}
		if spec.max > policy.maxMax {
			t.Errorf("%s max %d exceeds allow-list ceiling %d", spec.owner, spec.max, policy.maxMax)
		}
	}

	if canonicalCount == 0 {
		t.Fatal("no canonical (default=50, max=200) inputs sampled — the test is not actually covering the dominant path")
	}
}

// extractLimitTag reflects over an Input struct and parses the
// `query:"limit"` struct tag's minimum/maximum/default constraints.
// Returns present=false if the struct has no such field.
func extractLimitTag(input any) limitTagSpec {
	rt := reflect.TypeOf(input)
	owner := rt.PkgPath()
	if i := strings.LastIndex(owner, "/"); i >= 0 {
		owner = owner[i+1:]
	}
	owner = owner + "." + rt.Name()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := string(f.Tag)
		if !strings.Contains(tag, `query:"limit"`) {
			continue
		}
		return limitTagSpec{
			owner:   owner,
			def:     parseTagInt(tag, "default"),
			min:     parseTagInt(tag, "minimum"),
			max:     parseTagInt(tag, "maximum"),
			present: true,
		}
	}
	return limitTagSpec{owner: owner}
}

// parseTagInt extracts a numeric struct-tag value (e.g. `default:"50"`).
func parseTagInt(tag, key string) int {
	needle := key + `:"`
	idx := strings.Index(tag, needle)
	if idx < 0 {
		return 0
	}
	rest := tag[idx+len(needle):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return 0
	}
	v, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return v
}
