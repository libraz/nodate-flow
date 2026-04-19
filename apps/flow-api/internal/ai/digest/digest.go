// Package digest implements the "weekly digest" stream
// Given a slice of task snapshots for a workspace, it
// produces a deterministic markdown summary: counts by state, tasks
// completed in the last 7 days, and overdue open tasks.
//
// Like stateinfer and reminders, this is rule-based — the signals are
// already structured, so an LLM call adds cost without adding insight.
package digest

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// State mirrors tasks.derived_state.
type State string

// Derived state constants.
const (
	StateOpen      State = "open"
	StateWaiting   State = "waiting"
	StateReview    State = "review"
	StateDone      State = "done"
	StateCancelled State = "cancelled"
)

// TaskSnapshot is the compact projection the digest builder consumes.
type TaskSnapshot struct {
	TaskID      string
	Title       string
	State       State
	DueOn       time.Time
	HasDueOn    bool
	CompletedAt time.Time
	HasCompleted bool
}

// Digest is the aggregated output. Markdown is pre-rendered so the
// HTTP / MCP layers can return it verbatim.
type Digest struct {
	Counts            map[State]int  `json:"counts"`
	CompletedThisWeek []TaskSnapshot `json:"completedThisWeek"`
	OverdueOpen       []TaskSnapshot `json:"overdueOpen"`
	Markdown          string         `json:"markdown"`
}

const weekDays = 7

// Build walks the snapshots and produces a Digest. "Now" is injected
// so tests can pin the week window deterministically.
func Build(tasks []TaskSnapshot, now time.Time) Digest {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	weekAgo := now.Add(-weekDays * 24 * time.Hour)

	d := Digest{
		Counts:            map[State]int{},
		CompletedThisWeek: []TaskSnapshot{},
		OverdueOpen:       []TaskSnapshot{},
	}
	for _, t := range tasks {
		d.Counts[t.State]++
		if t.HasCompleted && !t.CompletedAt.Before(weekAgo) && t.State == StateDone {
			d.CompletedThisWeek = append(d.CompletedThisWeek, t)
		}
		if t.HasDueOn && t.DueOn.Before(now) &&
			(t.State == StateOpen || t.State == StateWaiting || t.State == StateReview) {
			d.OverdueOpen = append(d.OverdueOpen, t)
		}
	}
	sort.SliceStable(d.CompletedThisWeek, func(i, j int) bool {
		return d.CompletedThisWeek[i].CompletedAt.After(d.CompletedThisWeek[j].CompletedAt)
	})
	sort.SliceStable(d.OverdueOpen, func(i, j int) bool {
		return d.OverdueOpen[i].DueOn.Before(d.OverdueOpen[j].DueOn)
	})
	d.Markdown = renderMarkdown(d, now)
	return d
}

func renderMarkdown(d Digest, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Weekly digest — %s\n\n", now.Format("2006-01-02"))

	b.WriteString("## By state\n\n")
	order := []State{StateOpen, StateWaiting, StateReview, StateDone, StateCancelled}
	for _, s := range order {
		fmt.Fprintf(&b, "- **%s**: %d\n", s, d.Counts[s])
	}
	b.WriteString("\n")

	b.WriteString("## Completed this week\n\n")
	if len(d.CompletedThisWeek) == 0 {
		b.WriteString("_Nothing completed in the last 7 days._\n")
	} else {
		for _, t := range d.CompletedThisWeek {
			fmt.Fprintf(&b, "- %s — %s\n", t.Title, t.CompletedAt.Format("2006-01-02"))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Overdue\n\n")
	if len(d.OverdueOpen) == 0 {
		b.WriteString("_No overdue tasks._\n")
	} else {
		for _, t := range d.OverdueOpen {
			fmt.Fprintf(&b, "- %s — due %s\n", t.Title, t.DueOn.Format("2006-01-02"))
		}
	}
	return b.String()
}
