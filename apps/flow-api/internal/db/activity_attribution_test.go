package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestWorkspaceActivitySeparatesAgentsFromPeople guards the one column
// in v_workspace_activity that decides whose face the UI puts on a row.
//
// The MCP leg used to answer actor_kind from user_id alone, which made
// every agent tool call read as 'user' — and since an agent's token
// carries the id of whoever minted it, the timeline showed a person
// doing work an agent did. The ai leg had always branched on agent_id;
// the two legs disagreeing is what made it survive review.
//
// This is a text check because a view's semantics are not reachable
// without a server, and the property worth pinning — that the mcp leg
// asks about agent_id before it asks about user_id — is visible in the
// definition. Reordering the branches, or dropping the agent one, fails
// here.
func TestWorkspaceActivitySeparatesAgentsFromPeople(t *testing.T) {
	t.Parallel()

	body := readRepoFile(t, filepath.Join("sql", "flow", "views", "v_workspace_activity.sql"))
	leg := mcpLeg(t, body)

	agentAt := strings.Index(leg, "mi.agent_id IS NOT NULL")
	userAt := strings.Index(leg, "mi.user_id IS NOT NULL")

	if agentAt < 0 {
		t.Fatal("the mcp leg must decide actor_kind from mcp_invocations.agent_id; " +
			"without it every agent tool call is attributed to the human who minted the token")
	}
	if userAt < 0 {
		t.Fatal("the mcp leg must still fall back to the token owner for human-owned tokens")
	}
	if agentAt > userAt {
		t.Fatal("agent_id has to be tested before user_id: an agent token carries both, " +
			"so whichever is asked first is the actor the timeline reports")
	}
}

// TestMcpInvocationsRecordsItsActors requires the insert to bind both
// actor columns. A column the writer never populates is the state this
// started in: mcp_invocations.task_id was declared, indexed and
// documented while every row wrote NULL into it, so the audit question
// it existed to answer had no answer for as long as it shipped.
func TestMcpInvocationsRecordsItsActors(t *testing.T) {
	t.Parallel()

	body := readRepoFile(t, filepath.Join("sql", "queries", "mcp", "invocations.sql"))

	for _, col := range []string{"user_id", "agent_id", "task_id"} {
		if !regexp.MustCompile(`(?m)^\s*` + col + `,`).MatchString(body) {
			t.Errorf("LogMcpInvocation must write %s; a declared column no INSERT binds "+
				"reads as \"we don't know\" forever", col)
		}
	}
}

func mcpLeg(t *testing.T, view string) string {
	t.Helper()

	const marker = "FROM mcp_invocations mi"
	end := strings.Index(view, marker)
	if end < 0 {
		t.Fatal("v_workspace_activity no longer selects from mcp_invocations")
	}
	// The leg starts at the last UNION ALL before its FROM clause.
	start := strings.LastIndex(view[:end], "UNION ALL")
	if start < 0 {
		t.Fatal("could not locate the start of the mcp leg")
	}
	return view[start:end]
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	repo, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	// The path is built from a constant relative to this repository.
	b, err := os.ReadFile(filepath.Join(repo, rel)) //#nosec G304 -- fixed path under the repo root
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}
