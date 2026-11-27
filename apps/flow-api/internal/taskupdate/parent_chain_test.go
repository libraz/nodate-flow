package taskupdate

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func chainOf(parents map[uint32]uint32) parentLink {
	return func(id uint32) (sql.NullInt32, error) {
		p, ok := parents[id]
		return sql.NullInt32{Int32: int32(p), Valid: ok}, nil
	}
}

func TestCheckParentChain(t *testing.T) {
	t.Parallel()

	deep := map[uint32]uint32{}
	for i := uint32(2); i <= MaxParentDepth+10; i++ {
		deep[i] = i - 1
	}

	cases := []struct {
		name     string
		parents  map[uint32]uint32
		self     uint32
		proposed uint32
		want     error
	}{
		{name: "root parent", parents: map[uint32]uint32{}, self: 1, proposed: 2},
		{name: "self", parents: map[uint32]uint32{}, self: 1, proposed: 1, want: ErrParentCycle},
		{name: "child as parent", parents: map[uint32]uint32{2: 1}, self: 1, proposed: 2, want: ErrParentCycle},
		{name: "grandchild as parent", parents: map[uint32]uint32{2: 1, 3: 2}, self: 1, proposed: 3, want: ErrParentCycle},
		{name: "unrelated chain", parents: map[uint32]uint32{3: 2, 2: 4}, self: 1, proposed: 3},
		{name: "chain deeper than the cap", parents: deep, self: 1000, proposed: MaxParentDepth + 10, want: ErrParentCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkParentChain(tc.self, tc.proposed, chainOf(tc.parents))
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}
