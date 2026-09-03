// Package signaljudge — autonomy resolver interface and inert stub
// (ADR 0008 D4 / D7).
//
// The Applier consults an [AutonomyResolver] to translate a
// (workspace, signal_kind, confidence) triple into the
// suggest / draft / auto decision lattice. This file holds the
// interface plus a stub that always returns Suggest; cmd/api/main.go
// binds the production resolver in autonomy.go, which reads
// auto_action_rules + ai_settings.auto_action_threshold.
//
// Keeping the interface in this package avoids an import cycle: the
// Applier consumes the resolver, the autonomy implementation lives
// here so the Applier does not have to know about auto_action_rules
// internals.
package signaljudge

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// AutonomyLevel names the three branches the Applier supports.
// Mirrors signalkinds.Autonomy values verbatim so wire / DB / Applier
// stay aligned (suggest = surface, draft = create a draft for human
// review, auto = reify immediately).
type AutonomyLevel string

// Autonomy levels.
const (
	AutonomySuggest AutonomyLevel = "suggest"
	AutonomyDraft   AutonomyLevel = "draft"
	AutonomyAuto    AutonomyLevel = "auto"
)

// AutonomyDecision is the full autonomy resolution for one verdict.
// Returning a struct rather than a bare level lets rule references
// and further caps be added without breaking the Applier signature.
type AutonomyDecision struct {
	// Level is the resolved suggest / draft / auto branch.
	Level AutonomyLevel
	// MaxProposedEvents caps the number of [ProposedEvent] entries
	// the Applier reifies under autonomy=Auto. Zero means "no cap";
	// nothing populates it until auto_action_rules gains the column.
	MaxProposedEvents int
}

// AutonomyResolver maps (workspace, signal_kind, confidence) to the
// resolved autonomy decision. [SuggestOnlyResolver] is the inert
// default; [RuleBackedResolver] is the sqlc-backed production one.
type AutonomyResolver interface {
	Resolve(ctx context.Context, workspaceID uint32, kind signalkinds.Kind, confidence float64) (AutonomyDecision, error)
}

// SuggestOnlyResolver always returns AutonomySuggest. It lets the
// Applier wiring run end-to-end without depending on
// auto_action_rules; [RuleBackedResolver] drops in via the same
// interface.
type SuggestOnlyResolver struct{}

// Resolve implements [AutonomyResolver]. Ignores every input and
// returns AutonomySuggest with no proposed-event cap.
func (SuggestOnlyResolver) Resolve(_ context.Context, _ uint32, _ signalkinds.Kind, _ float64) (AutonomyDecision, error) {
	return AutonomyDecision{Level: AutonomySuggest}, nil
}
