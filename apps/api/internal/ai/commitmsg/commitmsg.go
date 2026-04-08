// Package commitmsg implements the "commit message proposal" stream
// (2.AI-8). Given a compact view of a change set — file paths, their
// status (added / modified / deleted), and an optional summary — it
// proposes a Conventional Commits message (type(scope): subject).
//
// Like the other deterministic engines in this tree (stateinfer,
// reminders, autoactions, inboxtriage, priorityopt), the v1 ruleset
// is pure Go: path prefixes map to scopes, path patterns map to
// commit types (feat / fix / docs / test / chore / build / refactor),
// and the subject is derived from the dominant change. A future LLM
// path can replace Propose() without changing callers.
package commitmsg

import (
	"fmt"
	"sort"
	"strings"
)

// Status describes the file-level change type in the source control
// sense. Unknown strings map to Modified via normalizeStatus.
type Status string

// Known statuses. Wire values match `git status --porcelain` letters
// loosely so callers do not have to translate.
const (
	StatusAdded    Status = "added"
	StatusModified Status = "modified"
	StatusDeleted  Status = "deleted"
	StatusRenamed  Status = "renamed"
)

// Change is one file in a candidate commit.
type Change struct {
	Path   string
	Status Status
}

// Proposal is the output of Propose. Type and Scope follow the
// Conventional Commits spec; Subject is a short imperative summary.
// Full is the ready-to-use message (e.g. "feat(web): add …").
type Proposal struct {
	Type    string
	Scope   string
	Subject string
	Full    string
}

// Propose runs the deterministic rules over changes and summary, and
// returns a single commit message proposal. It never returns a zero
// value: even an empty input produces a "chore: no changes" message
// so callers can display it without branching.
func Propose(changes []Change, summary string) Proposal {
	if len(changes) == 0 {
		return Proposal{
			Type:    "chore",
			Subject: "no changes",
			Full:    "chore: no changes",
		}
	}

	kind := inferType(changes)
	scope := inferScope(changes)
	subject := inferSubject(changes, summary)

	full := kind
	if scope != "" {
		full += "(" + scope + ")"
	}
	full += ": " + subject

	return Proposal{
		Type:    kind,
		Scope:   scope,
		Subject: subject,
		Full:    full,
	}
}

// inferType returns the Conventional Commits type that best fits the
// change set. Priorities (descending): test-only > docs-only >
// build/chore-only > refactor (pure rename) > fix (deletions heavy) >
// feat (default, any added file).
func inferType(changes []Change) string {
	var tests, docs, build, added, deleted, renamed, code int
	for _, c := range changes {
		p := strings.ToLower(c.Path)
		isTest := strings.Contains(p, "_test.") || strings.Contains(p, "/tests/") ||
			strings.Contains(p, ".test.") || strings.Contains(p, "/e2e/") ||
			strings.HasSuffix(p, ".spec.ts") || strings.HasSuffix(p, ".spec.tsx")
		switch {
		case isTest:
			tests++
		case strings.HasPrefix(p, "docs/") || strings.HasSuffix(p, ".md"):
			docs++
		case strings.HasPrefix(p, ".github/") || strings.HasPrefix(p, "infra/") ||
			strings.HasSuffix(p, "dockerfile") || strings.HasSuffix(p, "makefile") ||
			strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml"):
			build++
		default:
			code++
		}
		switch normalizeStatus(c.Status) {
		case StatusAdded:
			added++
		case StatusDeleted:
			deleted++
		case StatusRenamed:
			renamed++
		}
	}
	n := len(changes)
	switch {
	case tests == n:
		return "test"
	case docs == n:
		return "docs"
	case build == n:
		return "build"
	case renamed == n && added == 0 && deleted == 0:
		return "refactor"
	case deleted > added && code > 0:
		return "fix"
	case added > 0 && code > 0:
		return "feat"
	}
	return "chore"
}

// inferScope returns the top-level directory shared by the majority
// of the change set, with a few well-known folder normalizations
// (apps/web → web, apps/api → api, packages/sdk → sdk). When changes
// are spread across multiple top-levels, the scope is empty.
func inferScope(changes []Change) string {
	counts := make(map[string]int, 4)
	for _, c := range changes {
		counts[topLevelScope(c.Path)]++
	}
	// Pick the most common non-empty scope; require >=60% dominance.
	var best string
	var bestN int
	for k, v := range counts {
		if k == "" {
			continue
		}
		if v > bestN {
			best = k
			bestN = v
		}
	}
	if float32(bestN)/float32(len(changes)) < 0.60 {
		return ""
	}
	return best
}

func topLevelScope(path string) string {
	p := strings.TrimPrefix(path, "./")
	parts := strings.SplitN(p, "/", 3)
	if len(parts) == 0 {
		return ""
	}
	head := parts[0]
	if (head == "apps" || head == "packages") && len(parts) >= 2 {
		return parts[1]
	}
	return head
}

// inferSubject produces a short imperative subject. If the caller
// supplied a summary we use it verbatim (lower-cased, trailing
// period stripped); otherwise we describe the dominant change.
func inferSubject(changes []Change, summary string) string {
	s := strings.TrimSpace(summary)
	if s != "" {
		s = strings.TrimRight(s, ".")
		// Lowercase the first rune so it reads imperative.
		if len(s) > 0 {
			s = strings.ToLower(s[:1]) + s[1:]
		}
		if len(s) > 72 {
			s = s[:72]
		}
		return s
	}
	// Fallback: list up to 3 file basenames.
	names := make([]string, 0, len(changes))
	for _, c := range changes {
		base := c.Path
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		names = append(names, base)
	}
	sort.Strings(names)
	if len(names) > 3 {
		return fmt.Sprintf("update %s and %d more", strings.Join(names[:3], ", "), len(names)-3)
	}
	return "update " + strings.Join(names, ", ")
}

func normalizeStatus(s Status) Status {
	switch strings.ToLower(string(s)) {
	case "a", "added", "new":
		return StatusAdded
	case "d", "deleted", "removed":
		return StatusDeleted
	case "r", "renamed", "moved":
		return StatusRenamed
	default:
		return StatusModified
	}
}
