package pages

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// TestIsDuplicateEntry pins what the helper answers on: the MySQL error
// number, and nothing else. The messages below name keys that exist and
// are shaped the way MySQL shapes them, so a reader can see which write
// each case stands for — but the classification never reads them. A 1062
// naming any key on any table is a duplicate, and the caller decides
// which of its own constraints could have raised it.
func TestIsDuplicateEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "mysql error 1062 is duplicate",
			err: &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '7-0-Release process-1' for key " +
				"'pages.uniq_pages_workspace_id_scope_parent_page_id_title_active'"},
			want: true,
		},
		{
			// A 1062 raised by some other constraint entirely is still a
			// duplicate here. Callers in this package map every 1062 on a
			// page write to a taken title, which holds only because the
			// title key is the one such a write can realistically hit —
			// not because the helper checked. This case is what would
			// start failing if it were ever narrowed to one key name.
			name: "mysql error 1062 from another table is duplicate",
			err: &mysql.MySQLError{Number: 1062,
				Message: "Duplicate entry 'acme' for key 'workspaces.uniq_workspaces_slug'"},
			want: true,
		},
		{
			name: "mysql error 1062 with no message is duplicate",
			err:  &mysql.MySQLError{Number: 1062},
			want: true,
		},
		{
			name: "wrapped mysql error 1062 is duplicate",
			err:  fmt.Errorf("insert page: %w", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}),
			want: true,
		},
		{
			name: "different mysql error is not duplicate",
			err:  &mysql.MySQLError{Number: 1048, Message: "Column 'title' cannot be null"},
			want: false,
		},
		{
			name: "generic error is not duplicate",
			err:  errors.New("timeout"),
			want: false,
		},
		{
			name: "nil error is not duplicate",
			err:  nil,
			want: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isDuplicateEntry(tc.err)
			require.Equal(t, tc.want, got,
				"isDuplicateEntry(%v) = %v, want %v", tc.err, got, tc.want)
		})
	}
}
