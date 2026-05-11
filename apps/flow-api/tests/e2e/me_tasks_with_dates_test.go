package e2e

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file pin the input contract for GET /me/tasks-with-dates.
//
// History: an over-eager `pattern:"^20\\d{2}-(0[1-9]|1[0-2])-(0[1-9]|1\\d|2[0-8])$"`
// regex on the from/to query fields rejected day 29-31, silently 422'ing
// the calendar grid for any month with >28 days. These tests fail fast
// at the handler layer if the regex (or any equivalent over-narrow
// validator) is reintroduced.
//
// The handler under test is tasks.ListMyTasksWithDates
// (apps/flow-api/internal/http/handlers/tasks/me.go) which parses
// from/to with `time.Parse("2006-01-02", ...)`.

// meTasksWithDatesURL builds the cross-workspace /me/tasks-with-dates
// URL with from/to query params escaped.
func meTasksWithDatesURL(baseURL, from, to string) string {
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	return baseURL + "/me/tasks-with-dates?" + q.Encode()
}

// seedTaskWithDueOn creates a task in the given tenant's default project
// with the supplied due date. POST /tasks auto-attaches the caller as
// the sole `assignee`, so the task immediately surfaces in the caller's
// /me/tasks-with-dates feed without an explicit /actors call.
func seedTaskWithDueOnViaAPI(t *testing.T, accessToken, projectID, title, dueOn string) string {
	t.Helper()
	body := map[string]any{
		"projectId": projectID,
		"title":     title,
		"dueOn":     dueOn,
	}
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", accessToken, body, &resp)
	require.NotEmpty(t, resp.ID, "seed task create returned empty id")
	return resp.ID
}

// TestListMyTasksWithDates_HappyPath confirms the 2-arg date form
// returns the seeded task within range and excludes one outside.
func TestListMyTasksWithDates_HappyPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	inRangeID := seedTaskWithDueOnViaAPI(t, tt.AccessToken, tt.ProjectPublicID, "In range", "2026-04-15")
	_ = seedTaskWithDueOnViaAPI(t, tt.AccessToken, tt.ProjectPublicID, "Out of range", "2026-06-01")

	var resp struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			DueOn string `json:"dueOn"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		meTasksWithDatesURL(testServerURL, "2026-04-01", "2026-04-30"),
		tt.AccessToken, nil, &resp)

	foundIn := false
	for _, task := range resp.Tasks {
		if task.ID == inRangeID {
			foundIn = true
			assert.Equal(t, "2026-04-15", task.DueOn)
		}
		// Out-of-range task must NOT appear.
		assert.NotEqual(t, "2026-06-01", task.DueOn, "task outside range leaked into response")
	}
	assert.True(t, foundIn, "in-range task must surface in /me/tasks-with-dates; got %d tasks", len(resp.Tasks))
}

// TestListMyTasksWithDates_MonthEndDayBoundary is the direct regression
// guard for the rejected-regex bug. Days 29-31 must round-trip through
// the input validator. The regex used to be
// `^20\d{2}-(0[1-9]|1[0-2])-(0[1-9]|1\d|2[0-8])$` which rejected `30`
// and `31`.
func TestListMyTasksWithDates_MonthEndDayBoundary(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	cases := []struct {
		name     string
		from, to string
	}{
		{"day_30_to", "2026-04-01", "2026-04-30"},
		{"day_31_to", "2026-05-01", "2026-05-31"},   // the regex's headline reject
		{"day_29_from", "2026-04-29", "2026-05-02"}, // from at day 29 must also pass
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doJSONStatus(t, http.MethodGet,
				meTasksWithDatesURL(testServerURL, tc.from, tc.to),
				tt.AccessToken, nil)
			assert.Equal(t, http.StatusOK, status,
				"day boundary %s..%s must be accepted; body=%s", tc.from, tc.to, string(body))
		})
	}
}

// TestListMyTasksWithDates_UnparseableFromReturnsApiError pins the
// negative path: junk input must surface as VALIDATION.QUERY.FIELD_INVALID
// (status 422), not as a generic 500 or an internal sentinel string.
func TestListMyTasksWithDates_UnparseableFromReturnsApiError(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet,
		meTasksWithDatesURL(testServerURL, "not-a-date", "2026-05-01"),
		tt.AccessToken, nil)

	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(body))
	assert.Contains(t, string(body), "VALIDATION.QUERY.FIELD_INVALID",
		"unparseable from must surface VALIDATION.QUERY.FIELD_INVALID; body=%s", string(body))
}

// TestListMyTasksWithDates_ReversedRange pins the contract for a
// reversed range (from > to). The current handler returns 422 with
// VALIDATION.QUERY.FIELD_INVALID — see the `if to.Before(from)` branch
// in tasks/me.go. This test exists so the behavior cannot silently flip
// (e.g. swallow the reversal and return an empty list) without a test
// failure raising the alarm.
//
// NOTE for future reviewers: VALIDATION.QUERY.FIELD_INVALID is a
// generic code that doesn't tell the client *why* their query was
// rejected. If product wants to differentiate "unparseable" from
// "reversed", introduce a dedicated code (e.g.
// VALIDATION.QUERY.RANGE_REVERSED) and update this assertion. The
// status code itself (422) is the durable contract.
func TestListMyTasksWithDates_ReversedRange(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet,
		meTasksWithDatesURL(testServerURL, "2026-05-31", "2026-05-01"),
		tt.AccessToken, nil)

	assert.Equal(t, http.StatusUnprocessableEntity, status,
		"reversed range must be rejected with 422; body=%s", string(body))
	assert.Contains(t, string(body), "VALIDATION.QUERY.FIELD_INVALID",
		"reversed range must surface VALIDATION.QUERY.FIELD_INVALID; body=%s", string(body))
}
