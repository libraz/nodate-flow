package export

import (
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCSVWriteInterleavesConversionWithOutput is the behavioural guard on
// the CSV export's memory shape.
//
// The export ceiling is ten thousand rows, and the route used to convert
// all of them into DTOs before writing the first byte — on top of the
// query result it was converting from. Nothing about the file needs two
// rows at once: each is written and then unreachable.
//
// The property is that conversion and output interleave, and it is
// asserted by watching both: the sequence records when a row is built,
// the writer records how many rows had been built by the time it was
// handed bytes. If any write happens while some rows are still unbuilt,
// the file is being produced as it is converted. Collecting the sequence
// first — the mutation that undoes this — makes every write see a
// complete set, and the assertion fails.
func TestCSVWriteInterleavesConversionWithOutput(t *testing.T) {
	t.Parallel()

	// Enough rows, and wide enough, to overflow the csv writer's own
	// buffer several times over; a file that fits in the buffer reaches
	// the writer only at the final flush and shows no interleaving
	// either way.
	const rows = 300

	var built int
	var partialWrites int
	seq := func(yield func(ExportedTask) bool) {
		for i := range rows {
			built++
			if !yield(wideTask(i)) {
				return
			}
		}
	}
	w := writerFunc(func(p []byte) (int, error) {
		// The byte order mark goes out before any row is built; it says
		// nothing about interleaving, so only writes carrying rows count.
		if built > 0 && built < rows {
			partialWrites++
		}
		return len(p), nil
	})

	res := writeCSV(w, seq)

	require.NoError(t, res.err)
	require.Equal(t, rows, res.written)
	require.Equal(t, rows, built)
	require.Positive(t, partialWrites,
		"the CSV body must be written as rows are converted; every write saw all %d rows "+
			"already built, which means the whole export was materialised before the first byte", rows)
}

// TestCSVWriteStopsConvertingWhenTheTransportFails states the other half
// of the same property: a download that is cut short must stop costing
// the server. Because the rows are pulled one at a time, a failed write
// ends the sequence, and the rows behind it are never converted. With
// the set built up front they would all have been paid for regardless.
func TestCSVWriteStopsConvertingWhenTheTransportFails(t *testing.T) {
	t.Parallel()

	const rows = 300

	var built int
	seq := func(yield func(ExportedTask) bool) {
		for i := range rows {
			built++
			if !yield(wideTask(i)) {
				return
			}
		}
	}
	// Fail on the first write that carries rows rather than on the byte
	// order mark, so the failure is a transport that died mid-file.
	writes := 0
	w := writerFunc(func(p []byte) (int, error) {
		writes++
		if writes > 1 {
			return 0, fmt.Errorf("connection reset")
		}
		return len(p), nil
	})

	res := writeCSV(w, seq)

	require.Error(t, res.err)
	require.Less(t, built, rows,
		"a failed write must end the export; %d of %d rows were converted after the transport died",
		built, rows)
	require.LessOrEqual(t, res.written, built,
		"the reported count must never exceed the rows actually converted")
}

// wideTask builds one export row wide enough that a few dozen of them
// overflow the csv writer's buffer.
func wideTask(i int) ExportedTask {
	return ExportedTask{
		ID:          fmt.Sprintf("01HX00000000000000000000%02d", i%100),
		Title:       strings.Repeat("t", 200),
		Status:      "open",
		Priority:    1,
		ProjectID:   "01HX000000000000000000AAAA",
		ProjectName: strings.Repeat("p", 40),
		CreatedAt:   1700000000,
	}
}

// writerFunc adapts a function to io.Writer so a test can observe every
// write the csv writer makes.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// compile-time reminder that writeCSV pulls rather than receives: a
// slice cannot be passed here.
var _ func(writerFunc, iter.Seq[ExportedTask]) csvWriteResult = func(w writerFunc, s iter.Seq[ExportedTask]) csvWriteResult {
	return writeCSV(w, s)
}
