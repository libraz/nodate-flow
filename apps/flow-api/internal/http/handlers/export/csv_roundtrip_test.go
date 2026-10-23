package export

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/importer"
)

// A file written by the exporter, byte order mark and header labels
// included, must parse with the importer and carry the task fields back.
func TestWriteCSVOutputParsesWithImporter(t *testing.T) {
	t.Parallel()

	desc, due, started := "Body text", "2026-03-04", "2026-03-01"
	tasks := []ExportedTask{{
		ID: "01920000-0000-7000-8000-000000000001", Title: "Ship it", Description: &desc,
		Status: "open", Priority: 3, DueOn: &due, StartedOn: &started,
		ProjectID: "01920000-0000-7000-8000-0000000000ff", ProjectName: "Project", CreatedAt: 1_700_000_000,
	}}

	var buf bytes.Buffer
	res := writeCSV(&buf, slices.Values(tasks))
	require.NoError(t, res.err)

	rows, rowErrs, err := importer.ParseCSV(buf.String())
	require.NoError(t, err)
	require.Empty(t, rowErrs)
	require.Len(t, rows, 1)
	require.Equal(t, "Ship it", rows[0].Title)
	require.Equal(t, desc, rows[0].Description.String)
	require.EqualValues(t, 3, rows[0].Priority)
	require.Equal(t, due, rows[0].DueOn.Time.Format("2006-01-02"))
	require.Equal(t, started, rows[0].StartedOn.Time.Format("2006-01-02"))
}
