// Unit tests for the production [RuleBackedResolver]. The test suite
// substitutes the sqlc Queries struct with an in-memory rule table so
// the 5-layer resolution order is exercised without a real database.
package signaljudge

import (
	"context"
	"database/sql"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// fakeRule mirrors the subset of auto_action_rules columns the
// resolver actually reads. Kept narrow so test fixtures stay legible.
type fakeRule struct {
	kind          string
	signalKind    sql.NullString
	enabled       bool
	confidence    string
	createdAt     time.Time
	id            uint32
	autonomyLevel generated.NullAutoActionRulesAutonomyLevel
}

// fakeRuleMatcher is an in-memory [RuleMatcher] that mimics the
// MatchAutoActionRule SQL ordering: exact match > wildcard prefix >
// NULL fallback, then created_at ASC, then id ASC. Only enabled rows
// are eligible.
type fakeRuleMatcher struct {
	rules []fakeRule
}

func (f *fakeRuleMatcher) MatchAutoActionRule(_ context.Context, arg generated.MatchAutoActionRuleParams) (generated.MatchAutoActionRuleRow, error) {
	caller := ""
	if arg.SignalKind.Valid {
		caller = arg.SignalKind.String
	}

	// Score every eligible row; lower score wins (0 = exact, 1 =
	// wildcard prefix, 2 = NULL).
	type scored struct {
		rule  fakeRule
		score int
	}
	var hits []scored
	for _, r := range f.rules {
		if r.kind != arg.Kind || !r.enabled {
			continue
		}
		switch {
		case r.signalKind.Valid && r.signalKind.String == caller && caller != "":
			hits = append(hits, scored{rule: r, score: 0})
		case r.signalKind.Valid && strings.HasPrefix(r.signalKind.String, "*."):
			suffix := r.signalKind.String[2:]
			// Stored '*.presence' matches caller 'discord.presence':
			// the caller must end in '.<suffix>'.
			if strings.HasSuffix(caller, "."+suffix) {
				hits = append(hits, scored{rule: r, score: 1})
			}
		case !r.signalKind.Valid:
			hits = append(hits, scored{rule: r, score: 2})
		}
	}
	if len(hits) == 0 {
		return generated.MatchAutoActionRuleRow{}, sql.ErrNoRows
	}

	// Sort by (score asc, createdAt asc, id asc) without importing sort
	// for one slice: simple selection pass keeps the fixture obvious.
	best := hits[0]
	for _, h := range hits[1:] {
		if h.score < best.score {
			best = h
			continue
		}
		if h.score == best.score {
			if h.rule.createdAt.Before(best.rule.createdAt) {
				best = h
				continue
			}
			if h.rule.createdAt.Equal(best.rule.createdAt) && h.rule.id < best.rule.id {
				best = h
			}
		}
	}
	return generated.MatchAutoActionRuleRow{
		ID:            best.rule.id,
		Kind:          best.rule.kind,
		SignalKind:    best.rule.signalKind,
		Enabled:       best.rule.enabled,
		Confidence:    best.rule.confidence,
		CreatedAt:     best.rule.createdAt,
		AutonomyLevel: best.rule.autonomyLevel,
	}, nil
}

// fakeSettings is the in-memory [SettingsLookup] stand-in.
type fakeSettings struct {
	threshold float64
	present   bool
	err       error
}

func (f *fakeSettings) GetAutoActionThreshold(_ context.Context, _ uint32) (float64, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	return f.threshold, f.present, nil
}

// TestRuleBackedResolver_Resolve exhaustively walks the five layers
// plus the disabled-rule fall-through and deterministic tie-break.
func TestRuleBackedResolver_Resolve(t *testing.T) {
	t.Parallel()

	const ws = uint32(7)
	const auto = "escalate_overdue"

	t.Run("L1_exact_above_threshold_yields_auto", func(t *testing.T) {
		t.Parallel()
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{kind: auto, signalKind: sql.NullString{String: "discord.presence", Valid: true}, enabled: true, confidence: "0.80", id: 1},
			}},
			&fakeSettings{},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.95)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q", got.Level, AutonomyAuto)
		}
	})

	t.Run("L1_exact_below_threshold_yields_draft", func(t *testing.T) {
		t.Parallel()
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{kind: auto, signalKind: sql.NullString{String: "discord.presence", Valid: true}, enabled: true, confidence: "0.85", id: 1},
			}},
			&fakeSettings{threshold: 0.5, present: true},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.60)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyDraft {
			t.Fatalf("level = %q, want %q", got.Level, AutonomyDraft)
		}
	})

	t.Run("L1_disabled_falls_through_to_L4", func(t *testing.T) {
		t.Parallel()
		// fakeRuleMatcher skips disabled rows so the resolver sees
		// sql.ErrNoRows and consults ai_settings. The fixture also
		// exercises the defensive disabled-row branch by leaving
		// `enabled=false` on the in-memory row.
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{kind: auto, signalKind: sql.NullString{String: "discord.presence", Valid: true}, enabled: false, confidence: "0.99", id: 1},
			}},
			&fakeSettings{threshold: 0.40, present: true},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.50)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q (L4 threshold should fire)", got.Level, AutonomyAuto)
		}
	})

	t.Run("L2_wildcard_prefix_matches_dotted_suffix", func(t *testing.T) {
		t.Parallel()
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{kind: auto, signalKind: sql.NullString{String: "*.presence", Valid: true}, enabled: true, confidence: "0.50", id: 1},
			}},
			&fakeSettings{},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.75)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q (wildcard prefix should match discord.presence)", got.Level, AutonomyAuto)
		}
	})

	t.Run("L3_null_signal_kind_matches_when_nothing_more_specific", func(t *testing.T) {
		t.Parallel()
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{kind: auto, signalKind: sql.NullString{}, enabled: true, confidence: "0.30", id: 1},
			}},
			&fakeSettings{},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.40)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q (NULL fallback should match)", got.Level, AutonomyAuto)
		}
	})

	t.Run("L4_settings_threshold_applies_when_no_rule_matches", func(t *testing.T) {
		t.Parallel()
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: nil},
			&fakeSettings{threshold: 0.70, present: true},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.80)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q (L4 threshold should fire)", got.Level, AutonomyAuto)
		}
	})

	t.Run("L4_below_threshold_returns_suggest_not_yaml_default", func(t *testing.T) {
		t.Parallel()
		// Use Manual whose YAML default is "auto" so we can distinguish
		// "L4 suggest" from a fall-through to "L5 yaml default" — if the
		// code accidentally fell through, this test would see Auto.
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: nil},
			&fakeSettings{threshold: 0.90, present: true},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.Manual, 0.30)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomySuggest {
			t.Fatalf("level = %q, want %q (L4 below-threshold gate, not L5)", got.Level, AutonomySuggest)
		}
	})

	t.Run("L5_yaml_default_applies_when_no_rule_and_no_settings", func(t *testing.T) {
		t.Parallel()
		// Manual's YAML default is "auto" so we can distinguish layer
		// 5 from a generic fallback to AutonomySuggest.
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: nil},
			&fakeSettings{present: false},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.Manual, 0.10)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q (Manual.AutonomyDefault)", got.Level, AutonomyAuto)
		}
	})

	t.Run("L5_unknown_kind_falls_back_to_suggest", func(t *testing.T) {
		t.Parallel()
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: nil},
			&fakeSettings{present: false},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.Kind("nonsense.kind"), 1.0)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomySuggest {
			t.Fatalf("level = %q, want %q (unknown kind fallback)", got.Level, AutonomySuggest)
		}
	})

	t.Run("L1_exact_with_explicit_autonomy_level_overrides_confidence", func(t *testing.T) {
		t.Parallel()
		// Rule sets confidence at 0.99 (so a confidence-based gate would
		// fall to Draft), but autonomy_level = 'auto' should short-circuit
		// the gate and return Auto verbatim.
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{
					kind:       auto,
					signalKind: sql.NullString{String: "discord.presence", Valid: true},
					enabled:    true,
					confidence: "0.99",
					id:         1,
					autonomyLevel: generated.NullAutoActionRulesAutonomyLevel{
						AutoActionRulesAutonomyLevel: generated.AutoActionRulesAutonomyLevelAuto,
						Valid:                        true,
					},
				},
			}},
			&fakeSettings{},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.10)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q (explicit autonomy_level=auto must override low confidence)", got.Level, AutonomyAuto)
		}
	})

	t.Run("L1_exact_with_explicit_suggest_overrides_high_confidence", func(t *testing.T) {
		t.Parallel()
		// Same shape but autonomy_level = 'suggest'. Even at confidence
		// 0.99 (well above the rule's 0.50 gate) the explicit Suggest
		// override must win, preventing auto-act.
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{
					kind:       auto,
					signalKind: sql.NullString{String: "discord.presence", Valid: true},
					enabled:    true,
					confidence: "0.50",
					id:         1,
					autonomyLevel: generated.NullAutoActionRulesAutonomyLevel{
						AutoActionRulesAutonomyLevel: generated.AutoActionRulesAutonomyLevelSuggest,
						Valid:                        true,
					},
				},
			}},
			&fakeSettings{},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.99)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomySuggest {
			t.Fatalf("level = %q, want %q (explicit autonomy_level=suggest must override high confidence)", got.Level, AutonomySuggest)
		}
	})

	t.Run("L3_null_signal_kind_with_explicit_draft", func(t *testing.T) {
		t.Parallel()
		// NULL signal_kind fallback row carries autonomy_level = 'draft'.
		// Verdict confidence 0.99 would otherwise return Auto via the
		// 0.30 rule confidence; the explicit override forces Draft.
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{
					kind:       auto,
					signalKind: sql.NullString{},
					enabled:    true,
					confidence: "0.30",
					id:         1,
					autonomyLevel: generated.NullAutoActionRulesAutonomyLevel{
						AutoActionRulesAutonomyLevel: generated.AutoActionRulesAutonomyLevelDraft,
						Valid:                        true,
					},
				},
			}},
			&fakeSettings{},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.99)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyDraft {
			t.Fatalf("level = %q, want %q (explicit autonomy_level=draft on NULL signal_kind row)", got.Level, AutonomyDraft)
		}
	})

	t.Run("unknown_autonomy_level_falls_through_to_confidence", func(t *testing.T) {
		t.Parallel()
		// Defensive path: an unknown ENUM value (corrupted row or
		// schema drift) is treated as if autonomy_level were NULL.
		// Verdict 0.60 vs rule 0.50 should fire the confidence path
		// and return Auto.
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{
					kind:       auto,
					signalKind: sql.NullString{String: "discord.presence", Valid: true},
					enabled:    true,
					confidence: "0.50",
					id:         1,
					autonomyLevel: generated.NullAutoActionRulesAutonomyLevel{
						AutoActionRulesAutonomyLevel: generated.AutoActionRulesAutonomyLevel("bogus"),
						Valid:                        true,
					},
				},
			}},
			&fakeSettings{},
		)
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.60)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyAuto {
			t.Fatalf("level = %q, want %q (unknown autonomy_level must fall through to confidence path)", got.Level, AutonomyAuto)
		}
	})

	t.Run("deterministic_tie_break_picks_oldest_null_rule", func(t *testing.T) {
		t.Parallel()
		// Two NULL signal_kind rules with different created_at. The
		// older row wins. Confidence is deliberately different so we
		// can prove the older row was chosen.
		older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: []fakeRule{
				{kind: auto, signalKind: sql.NullString{}, enabled: true, confidence: "0.30", createdAt: newer, id: 2},
				{kind: auto, signalKind: sql.NullString{}, enabled: true, confidence: "0.95", createdAt: older, id: 1},
			}},
			&fakeSettings{},
		)
		// Confidence 0.50 is above the newer row's 0.30 but below the
		// older row's 0.95 — so the older row's higher threshold
		// forces Draft, proving it was the row consulted.
		got, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.50)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Level != AutonomyDraft {
			t.Fatalf("level = %q, want %q (older NULL row wins, 0.50 < 0.95)", got.Level, AutonomyDraft)
		}
	})

	t.Run("settings_error_surfaces", func(t *testing.T) {
		t.Parallel()
		sentinel := stderrors.New("settings boom")
		r := NewRuleBackedResolver(
			&fakeRuleMatcher{rules: nil},
			&fakeSettings{err: sentinel},
		)
		_, err := r.Resolve(context.Background(), ws, signalkinds.DiscordPresence, 0.50)
		if !stderrors.Is(err, sentinel) {
			t.Fatalf("err = %v, want sentinel", err)
		}
	})
}
