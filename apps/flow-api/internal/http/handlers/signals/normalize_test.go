// Package signals — unit tests for subject normalisation helpers shared
// by the manual ingestion handler and the provider webhook adapters.
package signals

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// TestResolveSubjectType locks in the three-tier precedence rule for
// subject_type resolution: explicit override beats the registry default,
// which beats the workspace fallback for unknown kinds. Without this
// rail, a webhook handler emitting a free-form kind (today: GitHub
// issue events) would land NULL in a NOT NULL column and crash at
// insert time.
func TestResolveSubjectType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     string
		override string
		want     generated.SignalsSubjectType
	}{
		{
			name:     "explicit override wins over kind default",
			kind:     "discord.presence", // registry default: user
			override: "task",
			want:     generated.SignalsSubjectTypeTask,
		},
		{
			name:     "registry kind falls back to its default",
			kind:     "discord.presence",
			override: "",
			want:     generated.SignalsSubjectTypeUser,
		},
		{
			name:     "calendar event kind resolves to calendar_event",
			kind:     "calendar.event_day_arrived",
			override: "",
			want:     generated.SignalsSubjectTypeCalendarEvent,
		},
		{
			name:     "unknown kind falls back to workspace",
			kind:     "github.issue.opened",
			override: "",
			want:     generated.SignalsSubjectTypeWorkspace,
		},
		{
			name:     "override applies even when kind is unknown",
			kind:     "some.unknown.kind",
			override: "user",
			want:     generated.SignalsSubjectTypeUser,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveSubjectType(tc.kind, tc.override)
			if got != tc.want {
				t.Fatalf("resolveSubjectType(%q, %q) = %q; want %q", tc.kind, tc.override, got, tc.want)
			}
		})
	}
}

// TestSubjectIDFor locks in the bookkeeping that subject_id is NULL for
// workspace-scoped signals and for any subject type the caller did not
// resolve an internal id for. Without this, an accidental zero internal
// id would write FK 0 to signals.subject_id and break the per-subject
// presence view (ADR 0008 D5).
func TestSubjectIDFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		subjectType generated.SignalsSubjectType
		internalID  int64
		wantValid   bool
		wantValue   int32
	}{
		{
			name:        "workspace subject is always NULL",
			subjectType: generated.SignalsSubjectTypeWorkspace,
			internalID:  42, // ignored on purpose
			wantValid:   false,
		},
		{
			name:        "zero internal id is NULL even for task subject",
			subjectType: generated.SignalsSubjectTypeTask,
			internalID:  0,
			wantValid:   false,
		},
		{
			name:        "task subject with non-zero id is set",
			subjectType: generated.SignalsSubjectTypeTask,
			internalID:  17,
			wantValid:   true,
			wantValue:   17,
		},
		{
			name:        "user subject with non-zero id is set",
			subjectType: generated.SignalsSubjectTypeUser,
			internalID:  3,
			wantValid:   true,
			wantValue:   3,
		},
		{
			name:        "calendar_event subject with non-zero id is set",
			subjectType: generated.SignalsSubjectTypeCalendarEvent,
			internalID:  9,
			wantValid:   true,
			wantValue:   9,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := subjectIDFor(tc.subjectType, tc.internalID)
			if got.Valid != tc.wantValid {
				t.Fatalf("subjectIDFor(%q, %d).Valid = %v; want %v", tc.subjectType, tc.internalID, got.Valid, tc.wantValid)
			}
			if tc.wantValid && got.Int32 != tc.wantValue {
				t.Fatalf("subjectIDFor(%q, %d).Int32 = %d; want %d", tc.subjectType, tc.internalID, got.Int32, tc.wantValue)
			}
		})
	}
}
