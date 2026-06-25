package tasks

import (
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/stretchr/testify/assert"
)

// edge is a tiny constructor to keep the table cases readable.
func edge(from, to uint32) generated.ListDependencyEdgesForWorkspaceRow {
	return generated.ListDependencyEdgesForWorkspaceRow{FromTaskID: from, ToTaskID: to}
}

// TestDependencyEdgeClosesCycle exercises the in-memory DAG guard used
// by AddDependency. The new edge under test is always from -> to.
func TestDependencyEdgeClosesCycle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		edges []generated.ListDependencyEdgesForWorkspaceRow
		from  uint32
		to    uint32
		want  bool
	}{
		{
			name:  "empty graph never cycles",
			edges: nil,
			from:  1, to: 2,
			want: false,
		},
		{
			name:  "direct back-edge closes a cycle",
			edges: []generated.ListDependencyEdgesForWorkspaceRow{edge(2, 1)},
			from:  1, to: 2, // 1->2 plus existing 2->1
			want: true,
		},
		{
			name:  "transitive back-edge closes a cycle",
			edges: []generated.ListDependencyEdgesForWorkspaceRow{edge(2, 3), edge(3, 1)},
			from:  1, to: 2, // 1->2->3->1
			want: true,
		},
		{
			name:  "parallel edge in same direction is fine",
			edges: []generated.ListDependencyEdgesForWorkspaceRow{edge(1, 2)},
			from:  1, to: 2,
			want: false,
		},
		{
			name:  "diamond without back-edge is a DAG",
			edges: []generated.ListDependencyEdgesForWorkspaceRow{edge(1, 2), edge(1, 3), edge(2, 4), edge(3, 4)},
			from:  4, to: 5,
			want: false,
		},
		{
			name:  "self edge is a cycle",
			edges: nil,
			from:  7, to: 7,
			want: true,
		},
		{
			name:  "pre-existing cycle in unrelated component terminates",
			edges: []generated.ListDependencyEdgesForWorkspaceRow{edge(10, 11), edge(11, 10)},
			from:  1, to: 2,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := dependencyEdgeClosesCycle(tc.edges, tc.from, tc.to)
			assert.Equal(t, tc.want, got)
		})
	}
}
