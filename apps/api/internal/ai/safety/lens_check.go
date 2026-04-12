// Package safety provides heuristic scanners that detect PII and
// secrets in user-generated content before it is exposed publicly.
package safety

import (
	"context"
	"regexp"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// Finding describes a single PII or secret match found during a scan.
type Finding struct {
	// TaskID is the public UUID of the task containing the match.
	TaskID string `json:"taskId"`
	// Field is the column that matched ("title" or "description").
	Field string `json:"field"`
	// Pattern is the heuristic category (e.g. "email_address", "api_key").
	Pattern string `json:"pattern"`
	// Snippet is a redacted excerpt showing the match in context.
	Snippet string `json:"snippet"`
}

// LensChecker scans task data visible through a public lens for PII and
// secrets before publishing. It uses regex-based heuristics (not LLM)
// for deterministic, fast results.
type LensChecker struct {
	Queries *generated.Queries
}

// NewLensChecker returns a LensChecker backed by the given query handle.
func NewLensChecker(q *generated.Queries) *LensChecker {
	return &LensChecker{Queries: q}
}

// heuristic pairs a human-readable pattern name with a compiled regexp.
type heuristic struct {
	name string
	re   *regexp.Regexp
}

// Heuristics are compiled once and shared across all Check invocations.
var heuristics = []heuristic{
	{name: "email_address", re: regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)},
	{name: "api_key", re: regexp.MustCompile(`(?:sk-|pk-|api[_\-]?key)[a-zA-Z0-9]{20,}`)},
	{name: "phone_number", re: regexp.MustCompile(`\+?[0-9]{1,4}[\s.\-]?\(?[0-9]{1,3}\)?[\s.\-]?[0-9]{3,4}[\s.\-]?[0-9]{3,4}`)},
	{name: "credit_card", re: regexp.MustCompile(`[0-9]{4}[\s\-]?[0-9]{4}[\s\-]?[0-9]{4}[\s\-]?[0-9]{4}`)},
	{name: "ssn", re: regexp.MustCompile(`[0-9]{3}-[0-9]{2}-[0-9]{4}`)},
}

// Check runs a heuristic scan on the task data visible through the
// given workspace (optionally scoped to a project). It returns a list
// of findings. If the list is non-empty the caller should warn the
// user before publishing.
//
// The limit parameter caps how many tasks are fetched for scanning.
// A reasonable default is 500.
func (c *LensChecker) Check(ctx context.Context, workspaceID uint32, projectID *uint32, limit int32) ([]Finding, error) {
	if limit <= 0 {
		limit = 500
	}

	var findings []Finding

	if projectID != nil {
		rows, err := c.Queries.ExportTasksForLens(ctx, generated.ExportTasksForLensParams{
			WorkspaceID: workspaceID,
			ProjectID:   *projectID,
			Limit:       limit,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			findings = append(findings, scanFields(r.PublicID.String(), r.Title, r.Description.String)...)
		}
	} else {
		rows, err := c.Queries.ExportTasksForWorkspace(ctx, generated.ExportTasksForWorkspaceParams{
			WorkspaceID: workspaceID,
			Limit:       limit,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			findings = append(findings, scanFields(r.PublicID.String(), r.Title, r.Description.String)...)
		}
	}

	return findings, nil
}

// scanFields checks a task's title and description against all heuristics.
func scanFields(taskID, title, description string) []Finding {
	var findings []Finding
	for _, h := range heuristics {
		if loc := h.re.FindStringIndex(title); loc != nil {
			findings = append(findings, Finding{
				TaskID:  taskID,
				Field:   "title",
				Pattern: h.name,
				Snippet: redactSnippet(title, loc),
			})
		}
		if description != "" {
			if loc := h.re.FindStringIndex(description); loc != nil {
				findings = append(findings, Finding{
					TaskID:  taskID,
					Field:   "description",
					Pattern: h.name,
					Snippet: redactSnippet(description, loc),
				})
			}
		}
	}
	return findings
}

// redactSnippet extracts a short window around the match and partially
// redacts the matched text, keeping only the first 3 and last 2
// characters visible.
func redactSnippet(text string, loc []int) string {
	const windowBefore = 20
	const windowAfter = 20

	start := loc[0] - windowBefore
	if start < 0 {
		start = 0
	}
	end := loc[1] + windowAfter
	if end > len(text) {
		end = len(text)
	}

	matched := text[loc[0]:loc[1]]
	redacted := redactMatch(matched)

	var b strings.Builder
	if start > 0 {
		b.WriteString("...")
	}
	b.WriteString(text[start:loc[0]])
	b.WriteString(redacted)
	b.WriteString(text[loc[1]:end])
	if end < len(text) {
		b.WriteString("...")
	}
	return b.String()
}

// redactMatch keeps the first 3 and last 2 characters of a matched
// string, replacing the middle with asterisks.
func redactMatch(s string) string {
	if len(s) <= 5 {
		return strings.Repeat("*", len(s))
	}
	return s[:3] + strings.Repeat("*", len(s)-5) + s[len(s)-2:]
}
