package helpers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sharedtesthelpers "github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
	"github.com/stretchr/testify/require"
)

// Exercise the generated DROP/CREATE script against an already populated
// schema. A fresh database does not catch a table whose filename differs
// from its CREATE name: the generated DROP silently misses the old table.
func TestGeneratedSchemaCanBeRebuiltTwice(t *testing.T) {
	sharedtesthelpers.SkipUnlessIntegration(t)
	inst := StartIsolated(t)
	root, err := repoRoot()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "sql", "schema.sql"))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := inst.DB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	statements := splitSQLStatements(string(raw))
	require.NotEmpty(t, statements)
	for round := 1; round <= 2; round++ {
		for i, statement := range statements {
			_, err := conn.ExecContext(ctx, statement)
			require.NoError(t, err, "schema rebuild round %d statement %d", round, i+1)
		}
	}
	for _, table := range []string{"ai_invocations_inputs", "ai_suggestion_batches_inputs", "task_submissions", "operation_receipts"} {
		var count int
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count))
		require.Equal(t, 1, count, "expected rebuilt table %s", table)
	}
}
