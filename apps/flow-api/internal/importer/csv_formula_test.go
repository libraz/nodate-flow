package importer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseCSVUndoesTheExportEscape covers the round trip between the
// two halves of this repository's own CSV.
//
// The export puts an apostrophe in front of any cell a spreadsheet
// would otherwise execute. Without the matching strip here, exporting a
// workspace and importing it back renames every task whose title starts
// with one of those characters, and the name grows another apostrophe
// on every round trip.
func TestParseCSVUndoesTheExportEscape(t *testing.T) {
	t.Parallel()

	// The formula cell is quoted and its inner quotes doubled, which is
	// how the export writes it: RFC 4180 quoting happens around the
	// escape, not instead of it.
	csv := "title,description\n" +
		`"'=HYPERLINK(""http://x"",""y"")",'+SUM(A1:A2)` + "\n" +
		"'-1+1,plain text\n"

	rows, rowErrs, err := ParseCSV(csv)
	require.NoError(t, err)
	require.Empty(t, rowErrs)
	require.Len(t, rows, 2)

	require.Equal(t, `=HYPERLINK("http://x","y")`, rows[0].Title,
		"the escape the export added must come back off")
	require.Equal(t, "+SUM(A1:A2)", rows[0].Description.String)
	require.Equal(t, "-1+1", rows[1].Title)
	require.Equal(t, "plain text", rows[1].Description.String)
}

// TestParseCSVKeepsAGenuineLeadingApostrophe pins the other side of the
// rule. A title someone actually wrote as `'tis the season` is not an
// escape, and stripping it would corrupt data in the name of undoing a
// corruption that never happened.
func TestParseCSVKeepsAGenuineLeadingApostrophe(t *testing.T) {
	t.Parallel()

	rows, rowErrs, err := ParseCSV("title\n'tis the season\n")
	require.NoError(t, err)
	require.Empty(t, rowErrs)
	require.Len(t, rows, 1)
	require.Equal(t, "'tis the season", rows[0].Title,
		"an apostrophe followed by ordinary text is part of the title")
}
