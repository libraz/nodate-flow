package eventlog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

func TestValidatePayloadIDsRejectsInternalKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload any
		wantIn  string
	}{
		{
			name:    "flat id key",
			payload: map[string]any{"taskId": uint32(42), "relation": "blocks"},
			wantIn:  "taskId",
		},
		{
			name:    "user id from a membership event",
			payload: map[string]any{"userId": 7, "oldRole": "owner"},
			wantIn:  "userId",
		},
		{
			name:    "bare id",
			payload: map[string]any{"id": int64(1)},
			wantIn:  "id",
		},
		{
			name:    "plural key",
			payload: map[string]any{"taskIds": []any{uint32(1), uint32(2)}},
			wantIn:  "taskIds",
		},
		{
			name:    "nested under a changed-row list",
			payload: map[string]any{"changes": []any{map[string]any{"eventId": 9}}},
			wantIn:  "changes.eventId",
		},
		{
			name:    "nested map",
			payload: map[string]any{"before": map[string]any{"assigneeId": 3}},
			wantIn:  "before.assigneeId",
		},
		{
			name:    "float carrier",
			payload: map[string]any{"targetTaskId": float64(12)},
			wantIn:  "targetTaskId",
		},
		{
			name:    "json.Number",
			payload: map[string]any{"sourceTaskId": json.Number("12")},
			wantIn:  "sourceTaskId",
		},
		{
			name:    "struct field",
			payload: struct{ TaskID uint32 }{TaskID: 5},
			wantIn:  "TaskID",
		},
		{
			name: "struct with a json tag",
			payload: struct {
				Task uint32 `json:"taskId"`
			}{Task: 5},
			wantIn: "taskId",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := eventlog.ValidatePayloadIDs(tc.payload)
			if err == nil {
				t.Fatalf("ValidatePayloadIDs(%#v) = nil, want an error naming %q", tc.payload, tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error %q does not name the offending field %q", err, tc.wantIn)
			}
		})
	}
}

func TestValidatePayloadIDsAcceptsPublicIdentifiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload any
	}{
		{name: "nil payload", payload: nil},
		{name: "empty map", payload: map[string]any{}},
		{
			name: "uuid strings",
			payload: map[string]any{
				"taskId":       "0198f3f1-0000-7000-8000-000000000001",
				"eventId":      "0198f3f1-0000-7000-8000-000000000002",
				"linkPublicId": "0198f3f1-0000-7000-8000-000000000003",
				"relation":     "blocks",
			},
		},
		{
			// Numbers are data unless the key names an identifier.
			name: "numeric non-id fields",
			payload: map[string]any{
				"confidence":  0.87,
				"count":       12,
				"durationMs":  1500,
				"attempt":     2,
				"reenabled":   true,
				"oldPriority": 3,
			},
		},
		{
			name:    "nested uuid list",
			payload: map[string]any{"taskIds": []any{"0198f3f1-0000-7000-8000-000000000001"}},
		},
		{
			// A key that merely contains "id" is not an identifier.
			name:    "id substring",
			payload: map[string]any{"idempotencyKey": 4, "valid": 1, "hidden": 2},
		},
		{
			name:    "nil value under an id key",
			payload: map[string]any{"taskId": nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := eventlog.ValidatePayloadIDs(tc.payload); err != nil {
				t.Fatalf("ValidatePayloadIDs(%#v) = %v, want nil", tc.payload, err)
			}
		})
	}
}

// TestValidatePayloadIDsNamesEveryOffender keeps the error usable: a
// payload built from several internal keys should report all of them, so
// the fix is one pass rather than one field per test run.
func TestValidatePayloadIDsNamesEveryOffender(t *testing.T) {
	t.Parallel()

	err := eventlog.ValidatePayloadIDs(map[string]any{
		"sourceTaskId": uint32(1),
		"targetTaskId": uint32(2),
		"kind":         "duplicates",
	})
	if err == nil {
		t.Fatal("ValidatePayloadIDs = nil, want an error")
	}
	for _, want := range []string{"sourceTaskId", "targetTaskId"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestRedactPayloadIDsDropsInternalKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "numeric task id is dropped",
			raw:  `{"taskId":42,"relation":"blocks"}`,
			want: `{"relation":"blocks"}`,
		},
		{
			name: "uuid task id survives",
			raw:  `{"taskId":"0198f3f1-0000-7000-8000-000000000001"}`,
			want: `{"taskId":"0198f3f1-0000-7000-8000-000000000001"}`,
		},
		{
			name: "membership payload keeps its roles",
			raw:  `{"userId":7,"oldRole":"owner","newRole":"member"}`,
			want: `{"newRole":"member","oldRole":"owner"}`,
		},
		{
			name: "non-id numbers survive",
			raw:  `{"confidence":0.87,"count":3}`,
			want: `{"confidence":0.87,"count":3}`,
		},
		{
			name: "nested map",
			raw:  `{"before":{"assigneeId":3,"title":"x"}}`,
			want: `{"before":{"title":"x"}}`,
		},
		{
			name: "list of internal ids goes with its key",
			raw:  `{"taskIds":[1,2],"kind":"batch"}`,
			want: `{"kind":"batch"}`,
		},
		{
			name: "unparseable payload fails closed",
			raw:  `{"taskId":`,
			want: `{}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := string(eventlog.RedactPayloadIDs([]byte(tc.raw)))
			if got != tc.want {
				t.Fatalf("RedactPayloadIDs(%s) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
}

// TestRedactPayloadIDsLeavesEmptyInputAlone keeps the NULL payload case
// distinguishable from an empty object on the wire.
func TestRedactPayloadIDsLeavesEmptyInputAlone(t *testing.T) {
	t.Parallel()

	if got := eventlog.RedactPayloadIDs(nil); got != nil {
		t.Fatalf("RedactPayloadIDs(nil) = %q, want nil", got)
	}
}

// TestRedactedPayloadPassesTheAppendRail ties the two halves together:
// whatever the redactor emits must satisfy the rail that guards writes,
// so the read path can never hand back something the write path would
// have refused.
func TestRedactedPayloadPassesTheAppendRail(t *testing.T) {
	t.Parallel()

	raw := `{"taskId":42,"changes":[{"eventId":9,"field":"due"}],"count":2}`
	var doc any
	if err := json.Unmarshal(eventlog.RedactPayloadIDs([]byte(raw)), &doc); err != nil {
		t.Fatalf("redacted payload is not valid JSON: %v", err)
	}
	if err := eventlog.ValidatePayloadIDs(doc); err != nil {
		t.Fatalf("redacted payload still trips the append rail: %v", err)
	}
}
