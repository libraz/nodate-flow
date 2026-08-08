package export

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errAfterNBytes is a writer that accepts a fixed number of bytes and
// then fails, standing in for the caller hanging up part way through a
// download.
type errAfterNBytes struct {
	budget int
	err    error
	sink   bytes.Buffer
}

func (w *errAfterNBytes) Write(p []byte) (int, error) {
	if w.budget <= 0 {
		return 0, w.err
	}
	n := len(p)
	if n > w.budget {
		n = w.budget
	}
	w.sink.Write(p[:n])
	w.budget -= n
	if n < len(p) {
		return n, w.err
	}
	return n, nil
}

func exportTasks(n int) []ExportedTask {
	tasks := make([]ExportedTask, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, ExportedTask{
			ID:          "01920000-0000-7000-8000-00000000000" + string(rune('a'+i%16)),
			Title:       strings.Repeat("t", 64),
			Status:      "open",
			Priority:    2,
			ProjectID:   "01920000-0000-7000-8000-0000000000ff",
			ProjectName: "Project",
			CreatedAt:   1_700_000_000,
		})
	}
	return tasks
}

// A caller that hangs up mid-download must leave a trace. The handler
// has already sent 200 by then, so if this error is dropped the only
// remaining record of the export is one that says every row was
// delivered.
func TestWriteCSVSurfacesAFailedWrite(t *testing.T) {
	t.Parallel()

	broken := errors.New("connection reset by peer")
	tasks := exportTasks(200)
	w := &errAfterNBytes{budget: 512, err: broken}

	res := writeCSV(w, tasks)

	require.Error(t, res.err, "a failed body write must be reported, not discarded")
	assert.ErrorIs(t, res.err, broken)
	assert.Less(t, res.written, len(tasks),
		"a write that failed after 512 bytes cannot have delivered all %d rows", len(tasks))
}

// csv.Writer buffers, so the last rows reach the transport only in
// Flush — and Flush returns nothing. A writer that fails only once the
// buffer drains is the case that slips past per-row error checks.
func TestWriteCSVSurfacesAFailedFlush(t *testing.T) {
	t.Parallel()

	broken := errors.New("broken pipe")
	// One row is far smaller than the csv.Writer's 4 KiB buffer, so
	// every Write is buffered and the only trip to the writer is the
	// Flush at the end.
	tasks := exportTasks(1)
	w := &errAfterNBytes{budget: 3, err: broken}

	res := writeCSV(w, tasks)

	require.Error(t, res.err, "cw.Error() after Flush is the only place a buffered failure shows up")
	assert.ErrorIs(t, res.err, broken)
}

func TestWriteCSVCountsEveryRowItWrote(t *testing.T) {
	t.Parallel()

	tasks := exportTasks(37)
	var buf bytes.Buffer

	res := writeCSV(&buf, tasks)

	require.NoError(t, res.err)
	assert.Equal(t, len(tasks), res.written)

	// Parse the file back so the count is checked against records, not
	// lines: a description holding a newline makes those differ.
	body := bytes.TrimPrefix(buf.Bytes(), []byte{0xEF, 0xBB, 0xBF})
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, len(tasks)+1, "the file holds one header row plus one row per task")
}

func TestWriteCSVReportsAWriterThatRefusesTheByteOrderMark(t *testing.T) {
	t.Parallel()

	broken := errors.New("closed")
	res := writeCSV(&errAfterNBytes{budget: 0, err: broken}, exportTasks(3))

	require.ErrorIs(t, res.err, broken)
	assert.Equal(t, 0, res.written)
}

// The audit trail must describe the download, not the query behind it.
func TestExportMetadataReportsWhatLeftNotWhatWasSelected(t *testing.T) {
	t.Parallel()

	meta := exportMetadata(5000, csvWriteResult{written: 12, err: io.ErrClosedPipe})

	assert.Equal(t, 12, meta["count"],
		"an export the caller cut off after 12 rows must not be recorded as 5000")
	assert.Equal(t, 5000, meta["selected"])
	assert.Equal(t, false, meta["complete"],
		"without this flag a short export is indistinguishable from a small workspace")
}

func TestExportMetadataMarksACleanExportComplete(t *testing.T) {
	t.Parallel()

	meta := exportMetadata(42, csvWriteResult{written: 42})

	assert.Equal(t, 42, meta["count"])
	assert.Equal(t, 42, meta["selected"])
	assert.Equal(t, true, meta["complete"])
	assert.Equal(t, "csv", meta["format"])
}
