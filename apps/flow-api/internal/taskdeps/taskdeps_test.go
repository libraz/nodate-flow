package taskdeps

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func blocks(from, to uint32) Edge { return Edge{From: from, To: to, Kind: "blocks"} }

// TestClosesCycle exercises the in-memory DAG guard. The new edge under
// test is always from -> to.
func TestClosesCycle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		edges []Edge
		from  uint32
		to    uint32
		kind  string
		want  bool
	}{
		{name: "empty graph never cycles", from: 1, to: 2, kind: "blocks"},
		{name: "direct back-edge closes a cycle", edges: []Edge{blocks(2, 1)}, from: 1, to: 2, kind: "blocks", want: true},
		{name: "transitive back-edge closes a cycle", edges: []Edge{blocks(2, 3), blocks(3, 1)}, from: 1, to: 2, kind: "blocks", want: true},
		{name: "parallel edge in same direction is fine", edges: []Edge{blocks(1, 2)}, from: 1, to: 2, kind: "blocks"},
		{
			name:  "diamond without back-edge is a DAG",
			edges: []Edge{blocks(1, 2), blocks(1, 3), blocks(2, 4), blocks(3, 4)},
			from:  4, to: 5, kind: "blocks",
		},
		{name: "self edge is a cycle", from: 7, to: 7, kind: "blocks", want: true},
		{
			name:  "pre-existing cycle in unrelated component terminates",
			edges: []Edge{blocks(10, 11), blocks(11, 10)},
			from:  1, to: 2, kind: "blocks",
		},
		{
			name:  "informational edge does not close a blocking cycle",
			edges: []Edge{{From: 2, To: 1, Kind: "relates"}},
			from:  1, to: 2, kind: "blocks",
		},
		{
			name:  "informational edge is never refused",
			edges: []Edge{blocks(2, 1)},
			from:  1, to: 2, kind: "relates",
		},
		{
			name:  "reciprocal duplicates are accepted",
			edges: []Edge{{From: 2, To: 1, Kind: "duplicates"}},
			from:  1, to: 2, kind: "duplicates",
		},
		{
			name:  "subtask_of takes part in the walk",
			edges: []Edge{{From: 2, To: 1, Kind: "subtask_of"}},
			from:  1, to: 2, kind: "blocks", want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ClosesCycle(tc.edges, tc.from, tc.to, tc.kind))
		})
	}
}
