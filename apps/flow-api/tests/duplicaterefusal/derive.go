// Package duplicaterefusal derives, from the committed SQL and the
// committed Go, every branch that catches MySQL's duplicate-entry error
// and turns it into a named refusal — and proves the write it guards sits
// on a key that can raise the conflict the refusal describes.
//
// A branch of this shape is a translation:
//
//	if isDuplicateEntry(err) {
//	    return nil, httpErr(apierrors.SomethingTaken)
//	}
//
// It states that ER_DUP_ENTRY on this write means one specific thing, and
// the API publishes that meaning as an error code a client is expected to
// act on. Whether the statement is true is a property of the table, not of
// the handler: the database can only raise the error the branch describes
// if some unique key on that table can be collided by what the caller
// supplied.
//
// Every table here carries a UUID v7 public_id, keyed by
// uniq_<table>_public_id and again by uniq_<table>_workspace_public_id. A
// key holding public_id is violated only when a server-generated UUID
// repeats, which is not the event any of these codes name — and if it ever
// did happen, the branch would report a retryable identifier collision as a
// permanent conflict over the caller's own input, which is the inversion
// the branch exists to prevent. So an identifier key is not a key this
// property counts. What counts is a key a caller can collide:
//
//	collidable  a UNIQUE key, or a PRIMARY KEY, none of whose columns is
//	            public_id, and which is not the AUTO_INCREMENT surrogate.
//	            The public_id exclusion needs no list of scope columns to
//	            go stale: public_id is unique on its own, so any wider key
//	            containing it is violated only when public_id repeats,
//	            whatever else the key holds.
//
// Both halves are derived rather than listed. The branches are read out of
// the Go, the keys out of sql/core/tables and sql/flow/tables — the files
// the schema is built from, rather than the generated dump, which is a
// build artefact that can lag the tables it was built from.
//
// The hard half is saying which table a branch guards. The branch tests an
// error variable, and the write that produced it is an assignment some
// lines above; resolving one to the other is what makes the check mean
// anything, and a site whose write cannot be resolved is reported by name
// rather than skipped. A skipped site is exactly how this class survived:
// the check would report full coverage of a set it did not read.
package duplicaterefusal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Statement is one named statement in sql/queries, normalised.
type Statement struct {
	// Name is the sqlc query name, which is also the generated method name
	// a caller invokes — and is therefore how a call site is matched to the
	// statement it performs.
	Name string
	// Path and Line locate its header.
	Path string
	Line int
	// SQL is the statement with comments stripped, lowercased and its
	// whitespace collapsed.
	SQL string
}

// Location renders the statement's position for a failure message.
func (s Statement) Location() string {
	return fmt.Sprintf("%s:%d", s.Path, s.Line)
}

// WriteTarget returns the table the statement writes a row into, and
// whether it writes one at all.
//
// A read is not a write target. A branch whose error came from a SELECT is
// guarding nothing a unique key can refuse, and reporting it as "attributed
// to the table it selected from" would answer a question nobody asked.
func (s Statement) WriteTarget() (string, bool) {
	fields := strings.Fields(s.SQL)
	take := func(i int) (string, bool) {
		if i >= len(fields) {
			return "", false
		}
		name := strings.Trim(strings.TrimRight(fields[i], "("), "`")
		if name == "" {
			return "", false
		}
		return name, true
	}
	switch {
	case len(fields) >= 2 && fields[0] == "insert" && fields[1] == "into":
		return take(2)
	case len(fields) >= 3 && fields[0] == "insert" && fields[1] == "ignore" && fields[2] == "into":
		return take(3)
	case len(fields) >= 2 && fields[0] == "replace" && fields[1] == "into":
		return take(2)
	case len(fields) >= 1 && fields[0] == "update":
		return take(1)
	default:
		return "", false
	}
}

// SuppressesDuplicate reports whether the statement asks MySQL not to raise
// the error the branch is written against.
//
// INSERT IGNORE downgrades a duplicate key to a warning and ON DUPLICATE
// KEY UPDATE turns it into an update. Either way the driver returns no
// error, so a branch guarding such a statement is as unreachable as one
// guarding a table with no collidable key — and unreachable for a reason a
// schema change can never fix.
func (s Statement) SuppressesDuplicate() bool {
	return strings.Contains(s.SQL, "on duplicate key update") ||
		strings.HasPrefix(s.SQL, "insert ignore")
}

var headerPattern = regexp.MustCompile(`^--\s*name:\s*(\S+)\s+:(\S+)`)

// RepoRoot returns the repository root, found by walking up from the
// caller's working directory to the go.work that defines the workspace.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("duplicaterefusal: go.work not found above the working directory")
		}
		dir = parent
	}
}

// Statements reads every named statement under sql/queries, keyed by the
// generated method name a call site spells.
//
// A name declared twice with different write targets is not resolved here:
// the entry keeps the first and the ambiguity surfaces at the call site,
// where it can be reported against the branch that relies on it.
func Statements(root string) (map[string]Statement, error) {
	out := map[string]Statement{}
	queries := filepath.Join(root, "sql", "queries")
	if _, err := os.Stat(queries); err != nil {
		return nil, err
	}
	err := filepath.WalkDir(queries, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, s := range parseQueryFile(filepath.ToSlash(rel), string(raw)) {
			if _, seen := out[s.Name]; !seen {
				out[s.Name] = s
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseQueryFile cuts a query file at its sqlc `-- name:` headers.
func parseQueryFile(path, text string) []Statement {
	var out []Statement
	var current *Statement
	var body []string

	flush := func() {
		if current == nil {
			return
		}
		current.SQL = normalizeSQL(strings.Join(body, "\n"))
		out = append(out, *current)
		current = nil
		body = nil
	}

	for i, line := range strings.Split(text, "\n") {
		if match := headerPattern.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			flush()
			current = &Statement{Name: match[1], Path: path, Line: i + 1}
			continue
		}
		if current == nil {
			continue
		}
		body = append(body, line)
	}
	flush()
	return out
}

// normalizeSQL strips comments, lowercases and collapses whitespace so a
// clause can be matched without caring how it was wrapped.
func normalizeSQL(body string) string {
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if code, _, found := strings.Cut(line, "--"); found {
			line = code
		}
		out.WriteString(line)
		out.WriteString(" ")
	}
	return strings.Join(strings.Fields(strings.ToLower(out.String())), " ")
}
