package importer

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// MaxCSVBytes and MaxCSVRows bound a single import.
//
// The ceiling is asymmetric with the export side, which returns up to
// 10,000 rows: a workspace exported at that size cannot be imported back
// in one job. The limits differ because the two sides carry their data
// differently — export streams a response body, while an import job
// keeps its payload in the config_json column of the queue row, and a
// queue table is the wrong place to park megabytes. Raising the import
// ceiling is a job for the attachment-backed payload described in
// [Row], not a bigger number here.
const (
	MaxCSVBytes = 1 << 20 // 1 MiB of CSV text
	MaxCSVRows  = 5000
)

// Column names accepted in the header row. They are the field names the
// task export emits, so a file produced by GET /export/tasks can be fed
// back in without renaming anything.
//
// status is deliberately absent. A task's state is derived from its
// event history, never written directly (the column is maintained by
// the transition path), so a CSV cannot dictate it. Imported tasks
// start in the default state and move from there like any other task.
// assigneeId is absent for a different reason: it would need public-id
// resolution plus a membership check per row, which is a decision about
// what to do with an unknown assignee rather than a parsing question.
const (
	colTitle       = "title"
	colDescription = "description"
	colPriority    = "priority"
	colDueOn       = "dueOn"
	colStartedOn   = "startedOn"
)

// Row is one parsed CSV line, shaped for taskcreate.Args.
type Row struct {
	// Line is the 1-based line number in the source text, counting the
	// header, so an error message points at what the user can see in
	// their file.
	Line        int
	Title       string
	Description sql.NullString
	Priority    int32
	DueOn       sql.NullTime
	StartedOn   sql.NullTime
}

// RowError is a per-row parse failure. The import records it against
// failed_items and carries on, because one malformed line out of a
// thousand is not a reason to reject the other 999.
type RowError struct {
	Line   int
	Column string
	Reason string
}

func (e RowError) Error() string {
	if e.Column == "" {
		return fmt.Sprintf("line %d: %s", e.Line, e.Reason)
	}
	return fmt.Sprintf("line %d: %s: %s", e.Line, e.Column, e.Reason)
}

// ErrNoCSVPayload is returned when the job config carries no CSV text.
var ErrNoCSVPayload = errors.New("importer: config_json has no csv field")

// ParseCSV reads the payload into rows plus the per-row failures found
// along the way. A returned error means the file as a whole could not be
// read — a missing header, an unparseable structure, a size beyond the
// ceiling — and the job fails without creating anything.
func ParseCSV(text string) ([]Row, []RowError, error) {
	// Spreadsheet exports commonly lead with a byte order mark, which
	// would otherwise become part of the first header name and hide the
	// title column.
	text = strings.TrimPrefix(text, "\xef\xbb\xbf")
	if strings.TrimSpace(text) == "" {
		return nil, nil, ErrNoCSVPayload
	}
	if len(text) > MaxCSVBytes {
		return nil, nil, fmt.Errorf("importer: csv payload is %d bytes, over the %d byte limit", len(text), MaxCSVBytes)
	}

	r := csv.NewReader(strings.NewReader(text))
	// Rows may legitimately differ in length when trailing optional
	// columns are omitted; the header lookup below handles short rows.
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("importer: read header: %w", err)
	}
	index, err := indexHeader(header)
	if err != nil {
		return nil, nil, err
	}

	var (
		rows     []Row
		rowErrs  []RowError
		lineNo   = 1 // the header
		titleCol = index[colTitle]
	)
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		lineNo++
		if err != nil {
			return nil, nil, fmt.Errorf("importer: read line %d: %w", lineNo, err)
		}
		if len(rows)+len(rowErrs) >= MaxCSVRows {
			return nil, nil, fmt.Errorf("importer: csv has more than %d rows", MaxCSVRows)
		}
		if isBlank(record) {
			continue
		}

		row := Row{Line: lineNo, Title: strings.TrimSpace(field(record, titleCol))}
		if row.Title == "" {
			rowErrs = append(rowErrs, RowError{Line: lineNo, Column: colTitle, Reason: "required"})
			continue
		}
		if rowErr := fillOptional(&row, record, index); rowErr != nil {
			rowErrs = append(rowErrs, *rowErr)
			continue
		}
		rows = append(rows, row)
	}

	return rows, rowErrs, nil
}

// fillOptional parses the optional columns onto row. The first bad
// column wins: reporting one reason per line keeps the error log
// readable when a whole file is malformed the same way.
func fillOptional(row *Row, record []string, index map[string]int) *RowError {
	if raw := strings.TrimSpace(field(record, index[colDescription])); raw != "" {
		row.Description = sql.NullString{String: raw, Valid: true}
	}
	if raw := strings.TrimSpace(field(record, index[colPriority])); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return &RowError{Line: row.Line, Column: colPriority, Reason: "not a number"}
		}
		row.Priority = int32(n)
	}
	for _, spec := range []struct {
		column string
		target *sql.NullTime
	}{
		{colDueOn, &row.DueOn},
		{colStartedOn, &row.StartedOn},
	} {
		raw := strings.TrimSpace(field(record, index[spec.column]))
		if raw == "" {
			continue
		}
		d, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			return &RowError{Line: row.Line, Column: spec.column, Reason: "not a YYYY-MM-DD date"}
		}
		*spec.target = sql.NullTime{Time: d, Valid: true}
	}
	return nil
}

// indexHeader maps each recognised column to its position. Unknown
// columns are ignored rather than rejected so a file exported with more
// fields than the importer consumes still loads.
func indexHeader(header []string) (map[string]int, error) {
	index := map[string]int{
		colTitle:       -1,
		colDescription: -1,
		colPriority:    -1,
		colDueOn:       -1,
		colStartedOn:   -1,
	}
	for i, raw := range header {
		name := strings.TrimSpace(raw)
		if _, known := index[name]; known {
			index[name] = i
		}
	}
	if index[colTitle] < 0 {
		return nil, fmt.Errorf("importer: csv header has no %q column", colTitle)
	}
	return index, nil
}

func field(record []string, i int) string {
	if i < 0 || i >= len(record) {
		return ""
	}
	return record[i]
}

func isBlank(record []string) bool {
	for _, v := range record {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}
