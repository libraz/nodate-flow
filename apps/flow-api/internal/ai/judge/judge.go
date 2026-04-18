// Package judge is the scaffold for the "LLM-as-judge" test harness
// (2.TEST-1). It gives the rest of the AI test suite a single place to
// describe quality expectations for a model output — required phrases,
// required JSON keys, a score floor — and evaluate a candidate answer
// against them without pulling in a live LLM provider.
//
// The v1 implementation is deterministic: rubrics are plain data, the
// judge iterates criteria, and the report is a structured pass/fail
// list. A future iteration can plug a small on-device model behind the
// same [Judge] interface for rubrics whose criteria are hard to
// express as pattern matches (e.g. "is this reasoning coherent?").
// Today's callers get stable, fast, offline scoring suitable for CI.
package judge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Criterion is one checkable expectation against a candidate response.
// Exactly one of the matcher fields should be set; the first non-empty
// matcher wins in evaluation order.
type Criterion struct {
	// Name is a short human-readable label for error reports.
	Name string
	// Weight determines this criterion's contribution to the final
	// score. Defaults to 1.0 when zero.
	Weight float32

	// Contains requires the candidate to contain all of these
	// substrings (case-insensitive).
	Contains []string
	// NotContains requires the candidate to contain none of these
	// substrings (case-insensitive).
	NotContains []string
	// RequiredJSONKeys requires the candidate to parse as JSON and to
	// contain every listed top-level key.
	RequiredJSONKeys []string
	// MinLength requires the candidate to be at least this many runes.
	MinLength int
	// MaxLength caps the candidate at this many runes.
	MaxLength int
}

// Rubric is a named collection of criteria. PassScore is the
// acceptance threshold; the default is 1.0 (every criterion must pass).
type Rubric struct {
	Name      string
	PassScore float32
	Criteria  []Criterion
}

// CriterionResult is one criterion's evaluation result.
type CriterionResult struct {
	Name   string
	Passed bool
	Reason string
	Weight float32
}

// Report is the full outcome of judging a candidate against a rubric.
type Report struct {
	RubricName string
	Passed     bool
	Score      float32
	Results    []CriterionResult
}

// Judge evaluates a candidate string against a rubric and returns a
// structured [Report]. Implementations must be pure and must not make
// network calls unless the caller wired a provider explicitly.
type Judge interface {
	Evaluate(rubric Rubric, candidate string) Report
}

// DeterministicJudge is the zero-dependency default. It runs each
// criterion as a pattern match and accumulates a weighted score.
type DeterministicJudge struct{}

// Evaluate implements [Judge] for the deterministic path.
func (DeterministicJudge) Evaluate(rubric Rubric, candidate string) Report {
	report := Report{
		RubricName: rubric.Name,
		Results:    make([]CriterionResult, 0, len(rubric.Criteria)),
	}
	var totalWeight float32
	var earned float32
	for _, c := range rubric.Criteria {
		w := c.Weight
		if w <= 0 {
			w = 1
		}
		totalWeight += w
		passed, reason := evaluateCriterion(c, candidate)
		if passed {
			earned += w
		}
		report.Results = append(report.Results, CriterionResult{
			Name:   c.Name,
			Passed: passed,
			Reason: reason,
			Weight: w,
		})
	}
	if totalWeight > 0 {
		report.Score = earned / totalWeight
	} else {
		// Vacuously true: nothing to check.
		report.Score = 1
	}
	pass := rubric.PassScore
	if pass <= 0 {
		pass = 1.0
	}
	report.Passed = report.Score >= pass
	return report
}

func evaluateCriterion(c Criterion, candidate string) (bool, string) {
	low := strings.ToLower(candidate)
	for _, s := range c.Contains {
		if !strings.Contains(low, strings.ToLower(s)) {
			return false, fmt.Sprintf("missing substring %q", s)
		}
	}
	for _, s := range c.NotContains {
		if strings.Contains(low, strings.ToLower(s)) {
			return false, fmt.Sprintf("forbidden substring %q present", s)
		}
	}
	if c.MinLength > 0 && runeLen(candidate) < c.MinLength {
		return false, fmt.Sprintf("length %d < minLength %d", runeLen(candidate), c.MinLength)
	}
	if c.MaxLength > 0 && runeLen(candidate) > c.MaxLength {
		return false, fmt.Sprintf("length %d > maxLength %d", runeLen(candidate), c.MaxLength)
	}
	if len(c.RequiredJSONKeys) > 0 {
		var obj map[string]any
		if err := json.Unmarshal([]byte(candidate), &obj); err != nil {
			return false, fmt.Sprintf("candidate is not valid JSON: %v", err)
		}
		for _, k := range c.RequiredJSONKeys {
			if _, ok := obj[k]; !ok {
				return false, fmt.Sprintf("missing JSON key %q", k)
			}
		}
	}
	return true, ""
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
