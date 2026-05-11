package tasks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildAgentContext_NoMemoNoAgent confirms the mapper omits the
// context entirely when the task has neither an assigned agent nor a
// persisted memo — keeping `agentContext` `omitempty` in the wire DTO
// so tasks without any agent history pay no payload cost.
func TestBuildAgentContext_NoMemoNoAgent(t *testing.T) {
	t.Parallel()
	got := buildAgentContext(nil, nil, "")
	assert.Nil(t, got)
}

// TestBuildAgentContext_NullMemoNoAgent treats a literal SQL JSON null
// payload identically to an absent column. Without this branch the
// mapper would surface a half-populated context just because the row's
// agent_memo column scanned as `"null"` bytes.
func TestBuildAgentContext_NullMemoNoAgent(t *testing.T) {
	t.Parallel()
	got := buildAgentContext([]byte("null"), nil, "")
	assert.Nil(t, got)
}

// TestBuildAgentContext_AgentOnly populates the Agent ref from the
// joined view columns even when the memo is unset, so the UI can
// render the assignee chip before the first run lands a memo row.
func TestBuildAgentContext_AgentOnly(t *testing.T) {
	t.Parallel()
	agentID := []byte{0x01, 0x8a, 0x6e, 0x2e, 0x59, 0xf3, 0x70, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}
	got := buildAgentContext(nil, agentID, "Planner")
	require.NotNil(t, got)
	require.NotNil(t, got.Agent)
	assert.Equal(t, "Planner", got.Agent.Name)
	assert.NotEmpty(t, got.Agent.ID)
	assert.Empty(t, got.LastThought)
	assert.Zero(t, got.Attempts)
	assert.Nil(t, got.LastRunAt)
}

// TestBuildAgentContext_MemoOnly captures the handed-back case: the
// task has no current agent assignee (we just detached one) but the
// memo from the last run still carries handoff metadata the UI needs.
func TestBuildAgentContext_MemoOnly(t *testing.T) {
	t.Parallel()
	memo := []byte(`{
	  "last_run_at": 1700000123,
	  "last_thought": "needs human review",
	  "attempts": 2,
	  "handoff_status": "handed_back",
	  "handoff_reason": "low_confidence"
	}`)
	got := buildAgentContext(memo, nil, "")
	require.NotNil(t, got)
	assert.Nil(t, got.Agent)
	require.NotNil(t, got.LastRunAt)
	assert.Equal(t, int64(1700000123), *got.LastRunAt)
	assert.Equal(t, "needs human review", got.LastThought)
	assert.Equal(t, 2, got.Attempts)
	assert.Equal(t, "handed_back", got.HandoffStatus)
	assert.Equal(t, "low_confidence", got.HandoffReason)
}

// TestBuildAgentContext_LastStartedAtFallback confirms the mapper
// falls back to last_started_at when last_run_at is absent. The
// orchestrator stamps started_at first, then overwrites with run_at on
// completion; mid-flight reads must still surface a timestamp.
func TestBuildAgentContext_LastStartedAtFallback(t *testing.T) {
	t.Parallel()
	memo := []byte(`{"last_started_at": 1700000099, "attempts": 1}`)
	got := buildAgentContext(memo, nil, "")
	require.NotNil(t, got)
	require.NotNil(t, got.LastRunAt)
	assert.Equal(t, int64(1700000099), *got.LastRunAt)
}

// TestBuildAgentContext_LastRunAtPreferredOverStarted documents the
// precedence rule: when both timestamps are present, last_run_at wins.
// Otherwise mid-flight runs would clobber the more recent completion
// time on the wire.
func TestBuildAgentContext_LastRunAtPreferredOverStarted(t *testing.T) {
	t.Parallel()
	memo := []byte(`{"last_started_at": 1, "last_run_at": 2}`)
	got := buildAgentContext(memo, nil, "")
	require.NotNil(t, got)
	require.NotNil(t, got.LastRunAt)
	assert.Equal(t, int64(2), *got.LastRunAt)
}

// TestBuildAgentContext_MalformedMemoTolerated guards against bad JSON
// in the column blowing up the whole task read. The mapper should
// surface the Agent ref (if present) and treat the unparseable memo as
// empty rather than 500-ing the entire response.
func TestBuildAgentContext_MalformedMemoTolerated(t *testing.T) {
	t.Parallel()
	agentID := []byte{0x01, 0x8a, 0x6e, 0x2e, 0x59, 0xf3, 0x70, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02}
	memo := []byte(`{not valid json`)
	got := buildAgentContext(memo, agentID, "Planner")
	require.NotNil(t, got)
	require.NotNil(t, got.Agent)
	assert.Equal(t, "Planner", got.Agent.Name)
	// Memo fields stay zero because Unmarshal failed silently.
	assert.Zero(t, got.Attempts)
	assert.Empty(t, got.HandoffStatus)
	assert.Nil(t, got.LastRunAt)
}
