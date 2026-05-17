// Package signaljudge — autonomy resolver interface and Phase 3 stub
// (ADR 0008 D4 / D7).
//
// The Applier consults an [AutonomyResolver] to translate a
// (workspace, signal_kind, confidence) triple into the
// suggest / draft / auto decision lattice. Phase 3 lands the
// interface and a stub that always returns Suggest; Phase 4 (A1)
// replaces the binding in cmd/api/main.go with a production resolver
// that reads auto_action_rules + ai_settings.auto_action_threshold.
//
// Keeping the interface in this package avoids an import cycle: the
// Applier consumes the resolver, the autonomy implementation lives
// here so the Applier does not have to know about auto_action_rules
// internals.
package signaljudge

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/signalkinds"
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
// Phase 3 only uses Level; Phase 4 may extend the struct with rule
// references / proposed-event caps without breaking the Applier
// signature (struct return keeps the contract stable).
type AutonomyDecision struct {
	// Level is the resolved suggest / draft / auto branch.
	Level AutonomyLevel
	// MaxProposedEvents caps the number of [ProposedEvent] entries
	// the Applier reifies under autonomy=Auto. Zero means "no cap";
	// Phase 4 will populate this from auto_action_rules.
	MaxProposedEvents int
}

// AutonomyResolver maps (workspace, signal_kind, confidence) to the
// resolved autonomy decision. Phase 3 ships [SuggestOnlyResolver];
// Phase 4 (A1) replaces it with a sqlc-backed implementation.
type AutonomyResolver interface {
	Resolve(ctx context.Context, workspaceID uint32, kind signalkinds.Kind, confidence float64) (AutonomyDecision, error)
}

// SuggestOnlyResolver always returns AutonomySuggest. Used during
// Phase 3 so the Applier wiring compiles end-to-end without yet
// depending on auto_action_rules. The production resolver in Phase 4
// will drop in via the same interface.
type SuggestOnlyResolver struct{}

// Resolve implements [AutonomyResolver]. Ignores every input and
// returns AutonomySuggest with no proposed-event cap.
func (SuggestOnlyResolver) Resolve(_ context.Context, _ uint32, _ signalkinds.Kind, _ float64) (AutonomyDecision, error) {
	return AutonomyDecision{Level: AutonomySuggest}, nil
}
