package apierr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
)

func TestDuplicateKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		wantKey string
		wantDup bool
	}{
		{
			name:    "table-qualified key",
			err:     &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '7-acme' for key 'projects.uniq_projects_workspace_id_slug'"},
			wantKey: "uniq_projects_workspace_id_slug",
			wantDup: true,
		},
		{
			name:    "unqualified key",
			err:     &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '1-2' for key 'uniq_task_dependencies_edge'"},
			wantKey: "uniq_task_dependencies_edge",
			wantDup: true,
		},
		{
			name:    "wrapped",
			err:     fmt.Errorf("insert: %w", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'x' for key 't.k'"}),
			wantKey: "k",
			wantDup: true,
		},
		{
			name:    "no message still a duplicate",
			err:     &mysql.MySQLError{Number: 1062},
			wantDup: true,
		},
		{
			name: "check violation is not a duplicate",
			err:  &mysql.MySQLError{Number: 3819, Message: "Check constraint 'chk_x' is violated."},
		},
		{
			name: "plain error",
			err:  errors.New("Duplicate entry 'x' for key 'k'"),
		},
		{
			name: "nil",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key, dup := DuplicateKey(tc.err)
			assert.Equal(t, tc.wantDup, dup)
			assert.Equal(t, tc.wantKey, key)
			assert.Equal(t, tc.wantDup, IsDuplicateEntry(tc.err))
		})
	}
}
