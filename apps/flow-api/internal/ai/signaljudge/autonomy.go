// Package signaljudge — production AutonomyResolver.
//
// [RuleBackedResolver] implements [AutonomyResolver] by consulting the
// five-layer resolution order documented in docs/conventions/autonomy.md:
//
//  1. exact auto_action_rules.signal_kind match for (workspace, *, kind).
//  2. wildcard-prefix rule (e.g. stored `*.presence` matches caller
//     `discord.presence`).
//  3. NULL signal_kind fallback row.
//  4. ai_settings.auto_action_threshold for the workspace.
//  5. signalkinds.Definition.AutonomyDefault from the YAML registry.
//
// Layers 1-3 are resolved by MatchAutoActionRule whose ORDER BY pins
// the most specific row first. The SQL is keyed on the auto-action
// rule kind (escalate_overdue / assign_owner / ...) which is
// orthogonal to the verdict's signal_kind, so the resolver fans the
// query across every known rule kind and picks the best result.
// Layers 4 and 5 are walked explicitly.
//
// Within L1-L3, if the matched row carries a non-NULL
// auto_action_rules.autonomy_level the resolver returns that value
// verbatim and skips the confidence-vs-threshold comparison entirely.
// NULL preserves the legacy confidence-based
// derivation so existing rules behave as before.
package signaljudge

import (
	"context"
	"database/sql"
	stderrors "errors"
	"log/slog"
	"strconv"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// ruleKinds enumerates every auto_action_rules.kind the autonomy
// resolver scans when resolving layers 1-3. Mirrors the enum on the
// PATCH /workspaces/{wsId}/ai/auto-action-rules handler so a new rule
// kind there is also automatically considered for autonomy.
var ruleKinds = []string{
	"escalate_overdue",
	"assign_owner",
	"nudge_assignee",
	"close_stale_review",
}

// RuleMatcher is the narrow surface [RuleBackedResolver] needs from
// the sqlc-generated Queries struct. Extracted as an interface so unit
// tests can substitute an in-memory rule table without spinning up a
// database.
type RuleMatcher interface {
	MatchAutoActionRule(ctx context.Context, arg generated.MatchAutoActionRuleParams) (generated.MatchAutoActionRuleRow, error)
}

// SettingsLookup is the narrow surface [RuleBackedResolver] needs to
// resolve the workspace-level fallback threshold (layer 4). Returning
// (_, false, nil) signals "no per-workspace override; fall through to
// layer 5". Returning a non-nil error aborts resolution.
type SettingsLookup interface {
	GetAutoActionThreshold(ctx context.Context, workspaceID uint32) (threshold float64, ok bool, err error)
}

// RuleBackedResolver is the production [AutonomyResolver]. It is safe
// for concurrent use: every dependency is read-only and the struct
// carries no mutable state.
type RuleBackedResolver struct {
	// Rules executes MatchAutoActionRule for layers 1-3.
	Rules RuleMatcher
	// Settings resolves the layer-4 ai_settings threshold. May be nil
	// in tests; nil collapses layer 4 to "no override".
	Settings SettingsLookup
}

// NewRuleBackedResolver constructs a resolver wired to the production
// dependencies. Both arguments are kept narrow (interface, not the
// full Queries / Settings types) so callers in tests can substitute
// fakes without an indirection layer.
func NewRuleBackedResolver(rules RuleMatcher, settings SettingsLookup) *RuleBackedResolver {
	return &RuleBackedResolver{Rules: rules, Settings: settings}
}

// matchedRule pairs a MatchAutoActionRuleRow with the specificity
// score the resolver attributes to it (0 = exact signal_kind, 1 =
// wildcard prefix, 2 = NULL fallback). Used internally for the
// cross-rule-kind comparison after the per-rule-kind SQL fan-out.
type matchedRule struct {
	row   generated.MatchAutoActionRuleRow
	score int
}

// Resolve implements [AutonomyResolver]. The 5-layer order is:
//
//   - L1-L3 (auto_action_rules): the resolver fans MatchAutoActionRule
//     across every known rule kind. The most specific row across all
//     kinds wins; ties break on created_at then id. If the matched row
//     has a non-NULL autonomy_level the resolver returns it verbatim
//     (operator-picked level via the matrix UI — confidence comparison
//     is bypassed). Otherwise a matched, enabled rule with verdict
//     confidence >= rule.confidence yields AutonomyAuto; below that it
//     yields AutonomyDraft (an operator set a rule but the LLM was not
//     confident enough to auto-act — surfacing a draft for human
//     review is the safer default).
//
//   - L4 (ai_settings.auto_action_threshold): if no rule matched and
//     the workspace has an override, AutonomyAuto when verdict
//     confidence >= threshold, AutonomySuggest below. Operators have
//     not configured a rule, so nothing should hit the draft review
//     queue from this layer; we only consult L5 when the workspace has
//     no ai_settings row at all.
//
//   - L5 (signalkinds YAML default): return Definition.AutonomyDefault
//     verbatim. Unknown kinds fall back to AutonomySuggest, the
//     safest level (no action, just surface).
//
// MaxProposedEvents stays 0 (no cap) until auto_action_rules gains a
// column to populate it from.
func (r *RuleBackedResolver) Resolve(ctx context.Context, workspaceID uint32, kind signalkinds.Kind, confidence float64) (AutonomyDecision, error) {
	if r == nil || r.Rules == nil {
		// Defensive: a nil resolver or missing dependency should not
		// crash the Applier; fall straight to the YAML default.
		return AutonomyDecision{Level: yamlDefault(kind)}, nil
	}

	// L1-L3: fan the per-rule-kind query and keep the best match.
	best, found, err := r.bestMatch(ctx, workspaceID, kind)
	if err != nil {
		return AutonomyDecision{}, err
	}
	if found {
		// Explicit override on the matched row short-circuits the
		// confidence gate. The column is an ENUM at the DB layer so an
		// unknown string should never appear in practice; defensively
		// fall through to the confidence path if it ever does, rather
		// than returning an invented level.
		if best.row.AutonomyLevel.Valid {
			if lvl, ok := autonomyLevelFromDB(best.row.AutonomyLevel.AutoActionRulesAutonomyLevel); ok {
				return AutonomyDecision{Level: lvl}, nil
			}
			slog.Default().DebugContext(ctx, "signaljudge: unknown autonomy_level on rule, falling through to confidence",
				slog.String("autonomy_level", string(best.row.AutonomyLevel.AutoActionRulesAutonomyLevel)),
				slog.Uint64("workspace_internal", uint64(workspaceID)),
			)
		}
		ruleConf := parseRuleConfidence(best.row.Confidence)
		if confidence >= ruleConf {
			return AutonomyDecision{Level: AutonomyAuto}, nil
		}
		return AutonomyDecision{Level: AutonomyDraft}, nil
	}

	// L4: workspace-level ai_settings threshold.
	if r.Settings != nil {
		threshold, ok, sErr := r.Settings.GetAutoActionThreshold(ctx, workspaceID)
		if sErr != nil {
			return AutonomyDecision{}, sErr
		}
		if ok {
			if confidence >= threshold {
				return AutonomyDecision{Level: AutonomyAuto}, nil
			}
			// Operators set an explicit threshold and confidence missed
			// it. Surface only — do not let the YAML default escalate
			// past the operator's gate.
			return AutonomyDecision{Level: AutonomySuggest}, nil
		}
	}

	// L5: YAML default for this signal kind.
	return AutonomyDecision{Level: yamlDefault(kind)}, nil
}

// bestMatch runs MatchAutoActionRule once per known rule kind and
// returns the single best (most specific, then oldest) enabled row.
// Returns (_, false, nil) when no rule kind produced a match.
//
// The fan-out is deliberate: MatchAutoActionRule filters on rule kind
// at the SQL layer, but the autonomy decision is orthogonal to which
// rule kind covers the signal. Iterating Go-side keeps the SQL stable
// and lets a future schema migration (e.g. an autonomy-specific row)
// drop in without rewriting the resolver.
func (r *RuleBackedResolver) bestMatch(ctx context.Context, workspaceID uint32, kind signalkinds.Kind) (matchedRule, bool, error) {
	signalKindArg := sql.NullString{String: string(kind), Valid: kind != ""}
	var best matchedRule
	found := false
	for _, rk := range ruleKinds {
		row, err := r.Rules.MatchAutoActionRule(ctx, generated.MatchAutoActionRuleParams{
			WorkspaceID: workspaceID,
			Kind:        rk,
			SignalKind:  signalKindArg,
		})
		switch {
		case err == nil && row.Enabled:
			// Re-derive the specificity score Go-side so the cross-rule-kind
			// comparison uses the same priority as the SQL CASE.
			score := matchScore(row.SignalKind, string(kind))
			if !found || replacesBest(best, row, score) {
				best = matchedRule{row: row, score: score}
				found = true
			}
		case err == nil && !row.Enabled:
			// Disabled rows leaked through (the SQL also filters; this
			// is belt-and-braces). Skip and keep scanning.
		case stderrors.Is(err, sql.ErrNoRows):
			// No matching row for this rule kind. Skip.
		default:
			return matchedRule{}, false, err
		}
	}
	return best, found, nil
}

// matchScore returns the specificity score for stored vs. caller
// signal_kind. Mirrors the CASE in MatchAutoActionRule:
//   - 0 = exact match
//   - 1 = wildcard-prefix match (stored '*.foo' vs caller '<x>.foo')
//   - 2 = NULL fallback (stored signal_kind is NULL)
func matchScore(stored sql.NullString, caller string) int {
	if !stored.Valid {
		return 2
	}
	if stored.String == caller {
		return 0
	}
	return 1
}

// replacesBest reports whether the candidate row should replace the
// current best match. Lower score wins; on tie, older created_at
// wins; on a created_at tie, lower id wins.
func replacesBest(best matchedRule, candidate generated.MatchAutoActionRuleRow, candidateScore int) bool {
	if candidateScore < best.score {
		return true
	}
	if candidateScore > best.score {
		return false
	}
	if candidate.CreatedAt.Before(best.row.CreatedAt) {
		return true
	}
	if candidate.CreatedAt.Equal(best.row.CreatedAt) && candidate.ID < best.row.ID {
		return true
	}
	return false
}

// autonomyLevelFromDB converts the sqlc-generated ENUM value to the
// resolver-facing [AutonomyLevel] constant. Returns (_, false) when the
// stored value does not match any of the three known levels; callers
// then fall through to the confidence-vs-threshold path rather than
// returning an invented level. The DB column is an ENUM so the (_,
// false) branch is purely defensive (think corrupted row or a schema
// drift).
func autonomyLevelFromDB(v generated.AutoActionRulesAutonomyLevel) (AutonomyLevel, bool) {
	switch v {
	case generated.AutoActionRulesAutonomyLevelSuggest:
		return AutonomySuggest, true
	case generated.AutoActionRulesAutonomyLevelDraft:
		return AutonomyDraft, true
	case generated.AutoActionRulesAutonomyLevelAuto:
		return AutonomyAuto, true
	default:
		return "", false
	}
}

// yamlDefault maps a signal kind to its YAML-declared AutonomyDefault
// (layer 5). Unknown kinds resolve to AutonomySuggest, the safest
// branch (no action; surface only).
func yamlDefault(kind signalkinds.Kind) AutonomyLevel {
	def, ok := signalkinds.Lookup(kind)
	if !ok {
		return AutonomySuggest
	}
	return AutonomyLevel(def.AutonomyDefault)
}

// parseRuleConfidence converts auto_action_rules.confidence (stored
// as DECIMAL string) to float64. A parse error collapses to 1.0 so a
// malformed row never accidentally triggers Auto.
func parseRuleConfidence(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 1.0
	}
	return v
}

// SQLSettingsLookup adapts the sqlc-generated Queries to the narrow
// [SettingsLookup] surface. Returns ok=false when the workspace has
// not written an ai_settings row yet so the resolver falls through to
// layer 5 (YAML default) rather than treating "missing" as a zero
// threshold.
type SQLSettingsLookup struct {
	Queries *generated.Queries
}

// GetAutoActionThreshold implements [SettingsLookup].
func (s *SQLSettingsLookup) GetAutoActionThreshold(ctx context.Context, workspaceID uint32) (float64, bool, error) {
	if s == nil || s.Queries == nil {
		return 0, false, nil
	}
	row, err := s.Queries.GetAiSettings(ctx, workspaceID)
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	v, perr := strconv.ParseFloat(row.AutoActionThreshold, 64)
	if perr != nil {
		// Malformed DECIMAL string is treated as absent so the
		// resolver does not crash on a single bad row.
		return 0, false, nil
	}
	return v, true, nil
}
