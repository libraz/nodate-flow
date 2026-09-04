package columnbounds

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Column is one schema column that can hold a string, together with how
// much of one it can hold.
type Column struct {
	Table string
	Name  string
	// Capacity is the largest string the column accepts, counted in the
	// same unit Units names.
	Capacity int
	// Units is "characters" for the declared-width types, whose width is a
	// character count regardless of the charset, and "bytes" for the blob
	// text types, whose limit is a byte count. A byte capacity bounds the
	// character count from above, so a declared bound above it cannot fit
	// under any encoding — which is the only thing this compares.
	Units string
	// Members are the values an ENUM column accepts, in the order it
	// declares them, and empty for every other type. A length says nothing
	// about them, so they are carried for the check that reads value sets
	// rather than used here.
	Members []string
}

// Qualified renders the column the way a failure names it.
func (c Column) Qualified() string { return c.Table + "." + c.Name }

// Schema is every column a value can be written into as a string, split by
// what constrains it, alongside the set of tables that exist at all.
type Schema struct {
	// Columns are the ones bounded by a length, keyed by table and column
	// name. They are what a declared maxLength is compared against.
	Columns map[string]map[string]Column
	// Enums are the ones bounded by a value set, keyed the same way. They
	// are kept apart from Columns because a length comparison against one
	// would report something no bound could fix.
	Enums  map[string]map[string]Column
	Tables map[string]bool
}

// EnumsOnly returns this schema with the ENUM columns standing where the
// length-bounded ones do.
//
// It exists so a check on which values a field accepts runs over the same
// resolution as the check on how long a value may be, rather than through a
// second copy of it. Resolution asks a schema which of a table's columns a
// wire name matches; swapping which columns it can answer with is the whole
// difference between the two questions.
func (s Schema) EnumsOnly() Schema {
	return Schema{Columns: s.Enums, Tables: s.Tables}
}

// Column returns one table's column and whether the schema declares it as a
// string-holding one.
func (s Schema) Column(table, column string) (Column, bool) {
	byName, ok := s.Columns[table]
	if !ok {
		return Column{}, false
	}
	c, ok := byName[column]
	return c, ok
}

// EnumColumn returns one table's column and whether the schema declares it
// as an ENUM.
func (s Schema) EnumColumn(table, column string) (Column, bool) {
	byName, ok := s.Enums[table]
	if !ok {
		return Column{}, false
	}
	c, ok := byName[column]
	return c, ok
}

// EnumCount returns how many ENUM columns the schema declares, which is
// what a caller asserts before trusting anything derived from them.
func (s Schema) EnumCount() int {
	n := 0
	for _, byName := range s.Enums {
		n += len(byName)
	}
	return n
}

// Count returns how many string-holding columns the schema declares, which
// is what a caller asserts before trusting anything derived from it.
func (s Schema) Count() int {
	n := 0
	for _, byName := range s.Columns {
		n += len(byName)
	}
	return n
}

// ReadSchema returns the contents of sql/schema.sql under root. The file is
// generated from sql/core and sql/flow, so it is the one place where every
// table's columns are visible at once.
func ReadSchema(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "sql", "schema.sql")) //#nosec G304,G122 -- repository path read at test time
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// blobText maps the text types to the number of bytes they hold.
var blobText = map[string]int{
	"TINYTEXT":   255,
	"TEXT":       65535,
	"MEDIUMTEXT": 16777215,
	"LONGTEXT":   4294967295,
}

// ParseSchema reads every column a string can be written into out of a
// schema dump, split by what constrains the string.
//
// Only the types whose constraint is stated by the definition are
// collected. VARCHAR and CHAR carry a character count, which is the same
// unit a wire bound is written in, so the comparison against them is exact.
// The text types carry a byte count, which bounds their character count
// from above. An ENUM carries neither: it names the values it takes, so it
// is kept under Enums, where the check that reads value sets finds it and a
// length comparison does not.
func ParseSchema(dump string) Schema {
	out := Schema{
		Columns: map[string]map[string]Column{},
		Enums:   map[string]map[string]Column{},
		Tables:  map[string]bool{},
	}
	for _, table := range parseCreateTables(dump) {
		out.Tables[table.name] = true
		for _, column := range table.columns {
			if members, ok := enumMembers(column.definition); ok {
				if _, seen := out.Enums[table.name]; !seen {
					out.Enums[table.name] = map[string]Column{}
				}
				out.Enums[table.name][column.name] = Column{
					Table: table.name,
					Name:  column.name,
					// The longest member is the longest string the column
					// can hold, so a length question about an ENUM has the
					// same answer shape as one about a VARCHAR.
					Capacity: longestRuneCount(members),
					Units:    "characters",
					Members:  members,
				}
				continue
			}
			capacity, units, ok := stringCapacity(column.definition)
			if !ok {
				continue
			}
			if _, seen := out.Columns[table.name]; !seen {
				out.Columns[table.name] = map[string]Column{}
			}
			out.Columns[table.name][column.name] = Column{
				Table:    table.name,
				Name:     column.name,
				Capacity: capacity,
				Units:    units,
			}
		}
	}
	return out
}

// enumMembers reads the values an ENUM column accepts, in the order the
// definition declares them, and reports false for every other type.
func enumMembers(definition string) ([]string, bool) {
	trimmed := strings.TrimSpace(definition)
	if !strings.HasPrefix(strings.ToUpper(trimmed), "ENUM(") {
		return nil, false
	}
	open := strings.IndexByte(trimmed, '(')
	end := matchingParen(trimmed, open+1)
	if end < 0 {
		return nil, false
	}

	var out []string
	inside := trimmed[open+1 : end]
	for i := 0; i < len(inside); {
		if inside[i] != '\'' {
			i++
			continue
		}
		next := skipString(inside, i)
		out = append(out, unquote(inside[i:next]))
		i = next
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// unquote turns one SQL string literal into the value it holds, undoing the
// ways a quote is written inside one.
func unquote(literal string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(literal, "'"), "'")
	return strings.NewReplacer(`\\`, `\`, `\'`, `'`, `''`, `'`).Replace(inner)
}

// longestRuneCount returns the character count of the longest value in a
// set.
func longestRuneCount(values []string) int {
	longest := 0
	for _, v := range values {
		if n := len([]rune(v)); n > longest {
			longest = n
		}
	}
	return longest
}

// stringCapacity reads the largest string a column definition accepts.
func stringCapacity(definition string) (capacity int, units string, ok bool) {
	upper := strings.ToUpper(strings.TrimSpace(definition))
	for _, keyword := range []string{"VARCHAR", "CHAR"} {
		rest, found := strings.CutPrefix(upper, keyword)
		if !found || !strings.HasPrefix(rest, "(") {
			continue
		}
		closeAt := strings.IndexByte(rest, ')')
		if closeAt < 0 {
			continue
		}
		width, err := strconv.Atoi(strings.TrimSpace(rest[1:closeAt]))
		if err != nil {
			continue
		}
		return width, "characters", true
	}
	for keyword, bytes := range blobText {
		if upper == keyword || strings.HasPrefix(upper, keyword+" ") {
			return bytes, "bytes", true
		}
	}
	return 0, "", false
}

// ---------------------------------------------------------------------
// Schema parsing
// ---------------------------------------------------------------------

type schemaTable struct {
	name    string
	columns []schemaColumn
}

type schemaColumn struct {
	name string
	// definition is everything after the name, so the type keywords can be
	// read off its front.
	definition string
}

// indexDefinitions are the table items that declare no column.
var indexDefinitions = []string{
	"PRIMARY KEY", "UNIQUE KEY", "UNIQUE INDEX", "KEY ", "INDEX ",
	"FULLTEXT", "SPATIAL", "CONSTRAINT", "CHECK ", "FOREIGN KEY",
}

// parseCreateTables splits every CREATE TABLE body into its columns. Views
// are not reached: they declare no widths of their own, and a write never
// lands in one.
//
// The scan tracks string literals and parentheses because column comments
// in this schema hold both parentheses and the `--` sequence, which a
// line-oriented split reads as structure.
func parseCreateTables(text string) []schemaTable {
	const marker = "CREATE TABLE "
	var tables []schemaTable
	for offset := 0; ; {
		idx := strings.Index(text[offset:], marker)
		if idx < 0 {
			return tables
		}
		start := offset + idx + len(marker)
		open := strings.Index(text[start:], "(")
		if open < 0 {
			return tables
		}
		name := strings.Trim(strings.TrimSpace(text[start:start+open]), "`")
		bodyStart := start + open + 1
		bodyEnd := matchingParen(text, bodyStart)
		if bodyEnd < 0 {
			return tables
		}
		table := schemaTable{name: name}
		for _, item := range splitTopLevel(text[bodyStart:bodyEnd]) {
			if column, ok := parseColumn(item); ok {
				table.columns = append(table.columns, column)
			}
		}
		tables = append(tables, table)
		offset = bodyEnd
	}
}

// parseColumn turns one table item into a column, or reports that the item
// declares an index rather than a column.
func parseColumn(item string) (schemaColumn, bool) {
	upper := strings.ToUpper(item)
	for _, prefix := range indexDefinitions {
		if strings.HasPrefix(upper, prefix) {
			return schemaColumn{}, false
		}
	}
	fields := strings.Fields(item)
	if len(fields) < 2 {
		return schemaColumn{}, false
	}
	definition := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item), fields[0]))
	return schemaColumn{name: strings.Trim(fields[0], "`"), definition: definition}, true
}

// matchingParen returns the index of the ")" closing the "(" that ends just
// before start, or -1.
func matchingParen(text string, start int) int {
	depth := 1
	for i := start; i < len(text); {
		switch {
		case text[i] == '\'':
			i = skipString(text, i)
		case strings.HasPrefix(text[i:], "/*"):
			i = skipUntil(text, i+2, "*/")
		case strings.HasPrefix(text[i:], "--"):
			i = skipUntil(text, i, "\n")
		case text[i] == '(':
			depth++
			i++
		case text[i] == ')':
			depth--
			if depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return -1
}

func skipString(text string, i int) int {
	for i++; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '\'' {
			if i+1 < len(text) && text[i+1] == '\'' {
				i++
				continue
			}
			return i + 1
		}
	}
	return i
}

func skipUntil(text string, i int, terminator string) int {
	end := strings.Index(text[i:], terminator)
	if end < 0 {
		return len(text)
	}
	return i + end + len(terminator)
}

// splitTopLevel splits a table body on the commas separating its
// definitions, dropping comments and ignoring commas nested inside
// parentheses.
//
// A string literal is stepped over whole and kept whole: the values an ENUM
// declares are literals, so dropping their text would leave the column
// saying it accepts nothing, while reading inside them would let a comma or
// a parenthesis in a column comment split the definition it belongs to.
func splitTopLevel(body string) []string {
	var items []string
	var current strings.Builder
	depth := 0
	for i := 0; i < len(body); {
		switch {
		case body[i] == '\'':
			end := skipString(body, i)
			current.WriteString(body[i:end])
			i = end
		case strings.HasPrefix(body[i:], "/*"):
			i = skipUntil(body, i+2, "*/")
		case strings.HasPrefix(body[i:], "--"):
			i = skipUntil(body, i, "\n")
		default:
			if body[i] == '(' {
				depth++
			}
			if body[i] == ')' {
				depth--
			}
			if body[i] == ',' && depth == 0 {
				items = appendItem(items, current.String())
				current.Reset()
				i++
				continue
			}
			current.WriteByte(body[i])
			i++
		}
	}
	return appendItem(items, current.String())
}

func appendItem(items []string, item string) []string {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return items
	}
	return append(items, trimmed)
}
