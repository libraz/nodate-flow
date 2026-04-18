package lenses

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
			err:  &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'foo' for key 'name'"},
			want: true,
		},
		{
			name: "wrapped mysql error 1062 is duplicate",
			err:  fmt.Errorf("insert failed: %w", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}),
			want: true,
		},
		{
			name: "mysql error 1451 is not duplicate",
			err:  &mysql.MySQLError{Number: 1451, Message: "Cannot delete or update a parent row"},
			want: false,
		},
		{
			name: "generic error is not duplicate",
			err:  errors.New("connection refused"),
			want: false,
		},
		{
			name: "nil error is not duplicate",
			err:  nil,
			want: false,
		},
		{
			name: "deeply wrapped 1062 is still detected",
			err: fmt.Errorf("outer: %w",
				fmt.Errorf("inner: %w",
					&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'bar'"})),
			want: true,
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
