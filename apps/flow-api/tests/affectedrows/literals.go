package affectedrows

// The other half of the statement universe: a write built as a Go string
// literal rather than declared in sql/queries.
//
// SQL belongs in sql/queries and reaches Go through sqlc, so a literal is
// the exception. That is exactly why it has to be read here. A derivation
// that matches only calls to generated statement methods covers a
// shrinking share of the removals as exceptions are added, and covers
// nothing at all in a package that keeps its SQL inline — coverage that
// falls as the code gets worse, silently, because a scope that stops
// matching subtracts findings rather than adding them.
//
// A literal is classified by the same shapes as a named statement: the
// text is normalized and handed to [Statement.RemovalKind], so a soft
// delete written inline and one written in sql/queries answer the same
// way, and a guarded claim stays outside the shape wherever it is
// written.

import (
	"fmt"
	"strconv"
	"strings"
)

// InlineAnnotation is the annotation an inline write is recorded under.
//
// A Go exec call hands back sql.Result, so the count is always there to
// read — which is what :execresult means for a named statement, and why
// [Statement.CountIsReachable] needs no separate rule for these.
const InlineAnnotation = "execresult"

// InlineStatement classifies one Go SQL string literal, and reports
// whether it carries the removal shape.
//
// path and line locate the literal, so a failure points at the write
// rather than at the query file the write is not in.
func InlineStatement(sql, path string, line int) (Statement, bool) {
	s := Statement{
		Annotation: InlineAnnotation,
		Path:       path,
		Line:       line,
		SQL:        normalize(sql),
	}
	switch s.RemovalKind() {
	case HardDelete:
		s.Name = describeInline("DELETE from", deleteTarget(s.SQL))
	case SoftDelete:
		s.Name = describeInline("soft delete of", updateTarget(s.SQL))
	case NotRemoval:
		return Statement{}, false
	}
	return s, true
}

// describeInline names an inline write the way a failure message reads
// it. There is no query name to quote, so the shape and the table stand
// in for one.
func describeInline(shape, table string) string {
	if table == "" {
		table = "an unreadable table"
	}
	return fmt.Sprintf("the inline %s %s", shape, table)
}

// deleteTarget returns the table a normalized DELETE removes from.
func deleteTarget(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) < 3 || fields[0] != "delete" || fields[1] != "from" {
		return ""
	}
	return fields[2]
}

// GoStringLiteral returns the text a Go string literal holds. Both
// spellings are read: the interpreted form the short one-line writes use,
// and the raw form the wrapped ones do.
func GoStringLiteral(value string) (string, bool) {
	text, err := strconv.Unquote(value)
	if err != nil {
		return "", false
	}
	return text, true
}
