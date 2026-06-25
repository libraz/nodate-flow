package signaljudge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/signalkinds"
)

// secretExcerpt embeds a token whose prefix is in the free-form
// blocklist (sk-ant-). After redaction the literal token must not
// survive anywhere it lands.
const secretExcerpt = "Closing this because the provider replied with sk-ant-deadbeefSECRET123 in the logs"

// recordingAppender captures every event the Applier emits so a test
// can assert on the persisted payloads.
type recordingAppender struct {
	events []eventbus.Event
}

func (r *recordingAppender) AppendJudgeEvent(_ context.Context, evt eventbus.Event) error {
	r.events = append(r.events, evt)
	return nil
}

// recordingMutator captures the comment body the Applier passes to the
// task-domain side effect.
type recordingMutator struct {
	commentBodies []string
}

func (m *recordingMutator) CompleteTask(_ context.Context, _ uint32, _ int64, _ uint32) error {
	return nil
}

func (m *recordingMutator) AddComment(_ context.Context, _ uint32, _ int64, _ uint32, body string) error {
	m.commentBodies = append(m.commentBodies, body)
	return nil
}

func (m *recordingMutator) DraftRetroTask(_ context.Context, _ uint32, _ int64, _ uint32, _ string, _ bool) (int64, string, error) {
	return 0, "", nil
}

// stubResolver resolves any public id to a fixed internal id.
type stubResolver struct{ id int64 }

func (s stubResolver) ResolveTask(_ context.Context, _ uint32, _ string) (int64, bool, error) {
	return s.id, true, nil
}

// fakeSignalUpdater swallows the signals UPDATE — the redaction
// assertions run against the event/comment paths, not the signals row.
type fakeSignalUpdater struct{}

func (fakeSignalUpdater) UpdateJudgeOutput(_ context.Context, _ int64, _ uint32, _ json.RawMessage, _ float64, _ *time.Time) error {
	return nil
}

// autoResolver forces AutonomyAuto so the materialised branches (which
// carry the excerpt into events and comments) actually run.
type autoResolver struct{}

func (autoResolver) Resolve(_ context.Context, _ uint32, _ signalkinds.Kind, _ float64) (AutonomyDecision, error) {
	return AutonomyDecision{Level: AutonomyAuto}, nil
}

func newTestApplier(rec *recordingAppender, mut *recordingMutator) *Applier {
	return &Applier{
		Bus:      rec,
		Tasks:    mut,
		Resolver: stubResolver{id: 42},
		Signals:  fakeSignalUpdater{},
		Autonomy: autoResolver{},
	}
}

func TestSanitizeVerdictRedactsReasoningExcerpt(t *testing.T) {
	in := Verdict{
		Action:           ActionNoop,
		Confidence:       0.5,
		ReasoningExcerpt: secretExcerpt,
	}
	out := SanitizeVerdict(in)
	if strings.Contains(out.ReasoningExcerpt, "sk-ant-deadbeefSECRET123") {
		t.Fatalf("token survived SanitizeVerdict: %q", out.ReasoningExcerpt)
	}
	if !strings.Contains(out.ReasoningExcerpt, redactionMarker) {
		t.Fatalf("expected redaction marker, got %q", out.ReasoningExcerpt)
	}
	// Input must not be mutated.
	if in.ReasoningExcerpt != secretExcerpt {
		t.Fatalf("SanitizeVerdict mutated its input: %q", in.ReasoningExcerpt)
	}
}

func TestSanitizeVerdictRedactsProposedEventPayload(t *testing.T) {
	payload := json.RawMessage(`{"note":"see sk-ant-deadbeefSECRET123","api_key":"super-secret-value"}`)
	in := Verdict{
		Action:           ActionNoop,
		Confidence:       0.5,
		ReasoningExcerpt: "ok",
		ProposedEvents: []ProposedEvent{
			{Type: eventbus.SignalJudged, PayloadJSON: payload},
		},
	}
	out := SanitizeVerdict(in)
	got := string(out.ProposedEvents[0].PayloadJSON)
	if strings.Contains(got, "sk-ant-deadbeefSECRET123") {
		t.Fatalf("free-form token survived in proposed-event payload: %q", got)
	}
	if strings.Contains(got, "super-secret-value") {
		t.Fatalf("api_key value survived in proposed-event payload: %q", got)
	}
	// Original input payload must be untouched.
	if !strings.Contains(string(in.ProposedEvents[0].PayloadJSON), "super-secret-value") {
		t.Fatalf("SanitizeVerdict mutated its input payload")
	}
}

// TestApplyRedactsExcerptInEventAndComment proves the secret never
// reaches the event log or a task comment on the add_comment auto
// branch: both the emitted event payloads and the comment body the
// Applier hands to the task mutator are redacted.
func TestApplyRedactsExcerptInEventAndComment(t *testing.T) {
	rec := &recordingAppender{}
	mut := &recordingMutator{}
	a := newTestApplier(rec, mut)

	target := "01HXXXXXXXXXXXXXXXXXXXXXXXX"
	verdict := Verdict{
		Action:             ActionAddComment,
		Confidence:         0.99,
		ReasoningExcerpt:   secretExcerpt,
		TargetTaskPublicID: &target,
	}

	if _, err := a.Apply(context.Background(), SignalRef{
		InternalID:  1,
		PublicID:    "sig-1",
		WorkspaceID: 7,
		Kind:        "task.idle",
	}, AgentRef{InternalID: 3}, 99, verdict); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(rec.events) == 0 {
		t.Fatal("no events recorded")
	}
	for _, evt := range rec.events {
		blob, _ := json.Marshal(evt.Payload)
		if strings.Contains(string(blob), "sk-ant-deadbeefSECRET123") {
			t.Fatalf("secret leaked into %q event payload: %s", evt.Type, blob)
		}
	}
	if len(mut.commentBodies) != 1 {
		t.Fatalf("expected one comment, got %d", len(mut.commentBodies))
	}
	if strings.Contains(mut.commentBodies[0], "sk-ant-deadbeefSECRET123") {
		t.Fatalf("secret leaked into comment body: %q", mut.commentBodies[0])
	}
	if !strings.Contains(mut.commentBodies[0], redactionMarker) {
		t.Fatalf("comment body not redacted: %q", mut.commentBodies[0])
	}
}
