package pages

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestIsDuplicateEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "mysql error 1062 is duplicate",
			err:  &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'title' for key 'pages.ux_pages_title'"},
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
