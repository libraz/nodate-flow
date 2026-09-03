package ai

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// --- DTOs ---

// AutoActionRuleBody is the public representation of a single
// auto-action rule. Kind identifies the rule family; SignalKind narrows
// it to a specific signal scope (nil = wildcard, matches every signal
// kind for that family). See docs/conventions/autonomy.md for the
// five-layer resolution order.
//
// AutonomyLevel, when non-nil, is the operator-picked override for the
// resolver: the autonomy level is returned verbatim and the
// confidence-vs-threshold comparison is bypassed. nil means "use the
// legacy confidence-based derivation" (default).
type AutoActionRuleBody struct {
	Kind          string  `json:"kind"`
	SignalKind    *string `json:"signalKind,omitempty"`
	Enabled       bool    `json:"enabled"`
	Confidence    float64 `json:"confidence"`
	IdleHours     int     `json:"idleHours"`
	AutonomyLevel *string `json:"autonomyLevel,omitempty" enum:"suggest,draft,auto" doc:"Operator-picked autonomy level override. When set the resolver returns this level verbatim and skips the confidence gate. Nil/omitted means the row uses confidence-based derivation."`
}

// GetAutoActionRulesInput is the path-only input for
// GET /workspaces/{wsId}/ai/auto-action-rules.
type GetAutoActionRulesInput struct {
	WsID string `path:"wsId"`
}

// GetAutoActionRulesOutput is the response for
// GET /workspaces/{wsId}/ai/auto-action-rules.
type GetAutoActionRulesOutput struct {
	Body struct {
		Rules []AutoActionRuleBody `json:"rules"`
	}
}

// PatchAutoActionRulesInput is the body for
// PATCH /workspaces/{wsId}/ai/auto-action-rules.
type PatchAutoActionRulesInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Rules []PatchAutoActionRuleItem `json:"rules"`
	}
}

// PatchAutoActionRuleItem represents a partial update to a single
// auto-action rule. Only non-nil fields are applied. SignalKind is the
// rule's scope address — nil targets the wildcard row, a dotted string
// targets that exact signal kind, and a wildcard-prefix value (e.g.
// `*.presence`) targets a single wildcard-prefix row.
//
// AutonomyLevel: omitting the field preserves the row's prior value
// (standard PATCH semantic). Sending one of "suggest" / "draft" /
// "auto" writes that level as an explicit override on the resolver.
// Clearing an override back to NULL is intentionally NOT supported by
// this PATCH today — the matrix UI always picks one of the three
// levels. A clear-to-NULL flow (e.g. ClearAutonomyLevel companion
// bool) is a follow-up if needed.
type PatchAutoActionRuleItem struct {
	Kind          string   `json:"kind" enum:"escalate_overdue,assign_owner,nudge_assignee,close_stale_review"`
	SignalKind    *string  `json:"signalKind,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
	Confidence    *float64 `json:"confidence,omitempty" minimum:"0" maximum:"1"`
	IdleHours     *int     `json:"idleHours,omitempty" minimum:"0" maximum:"8760"`
	AutonomyLevel *string  `json:"autonomyLevel,omitempty" enum:"suggest,draft,auto" doc:"Operator-picked autonomy level override. Omit to preserve the prior value. Clearing back to NULL is not supported by this PATCH."`
}

// PatchAutoActionRulesOutput is the response for
// PATCH /workspaces/{wsId}/ai/auto-action-rules.
type PatchAutoActionRulesOutput struct {
	Body struct {
		Rules []AutoActionRuleBody `json:"rules"`
	}
}

// --- default seed values ---

type ruleDefault struct {
	kind       string
	enabled    bool
	confidence string
	idleHours  uint32
}

var defaultRules = []ruleDefault{
	{kind: "escalate_overdue", enabled: true, confidence: "0.85", idleHours: 0},
	{kind: "assign_owner", enabled: true, confidence: "0.75", idleHours: 24},
	{kind: "nudge_assignee", enabled: true, confidence: "0.70", idleHours: 72},
	{kind: "close_stale_review", enabled: true, confidence: "0.70", idleHours: 120},
}

// ruleKey is the composite address of an auto_action_rules row.
// Mirrors the UNIQUE KEY (workspace_id, kind, signal_kind_match) — the
// existing-rule lookup must be exact on both columns because a
// different signal_kind targets a distinct row by design.
type ruleKey struct {
	Kind       string
	SignalKind sql.NullString
}

// --- handlers ---

// GetAutoActionRules returns the workspace's auto-action rules. When no
// rules exist for the workspace, the four default rows are seeded via
// INSERT IGNORE (UpsertAutoActionRule) before returning them.
func GetAutoActionRules(deps Deps) func(context.Context, *GetAutoActionRulesInput) (*GetAutoActionRulesOutput, error) {
	return func(ctx context.Context, _ *GetAutoActionRulesInput) (*GetAutoActionRulesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		rows, err := deps.Queries.ListAutoActionRulesForWorkspace(ctx, ws.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Seed defaults when no rows exist yet.
		if len(rows) == 0 {
			if err := seedDefaultRules(ctx, deps.Queries, ws.ID); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rows, err = deps.Queries.ListAutoActionRulesForWorkspace(ctx, ws.ID)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		out := &GetAutoActionRulesOutput{}
		out.Body.Rules = mapRuleRows(rows)
		return out, nil
	}
}

// PatchAutoActionRules applies partial updates to one or more auto-action
// rules. For each item the current rule (or default) is read, patch fields
// are merged, and the result is upserted. The full list of all 4 rules is
// returned after the update.
func PatchAutoActionRules(deps Deps) func(context.Context, *PatchAutoActionRulesInput) (*PatchAutoActionRulesOutput, error) {
	return func(ctx context.Context, in *PatchAutoActionRulesInput) (*PatchAutoActionRulesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Read current rules into a map keyed by (kind, signal_kind).
		// Rules are now per-signal-kind, so kind alone is no longer a
		// unique address.
		rows, err := deps.Queries.ListAutoActionRulesForWorkspace(ctx, ws.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		existing := make(map[ruleKey]generated.ListAutoActionRulesForWorkspaceRow, len(rows))
		for _, r := range rows {
			existing[ruleKey{Kind: r.Kind, SignalKind: r.SignalKind}] = r
		}

		// Apply each patch item.
		for _, item := range in.Body.Rules {
			params := resolveRuleParams(ws.ID, item, existing)
			if err := deps.Queries.UpsertAutoActionRule(ctx, params); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		// Re-read and ensure all 4 wildcard defaults exist.
		rows, err = deps.Queries.ListAutoActionRulesForWorkspace(ctx, ws.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if !hasAllDefaults(rows) {
			if err := seedDefaultRules(ctx, deps.Queries, ws.ID); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rows, err = deps.Queries.ListAutoActionRulesForWorkspace(ctx, ws.ID)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		out := &PatchAutoActionRulesOutput{}
		out.Body.Rules = mapRuleRows(rows)
		return out, nil
	}
}

// RegisterAutoActionRules wires the auto-action rules GET and PATCH
// endpoints. The caller MUST attach RequireWorkspaceMember +
// RequireWorkspaceRole(Admin) to the underlying chi router.
func RegisterAutoActionRules(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "ai-auto-action-rules-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/auto-action-rules",
		Summary:     "List auto-action rules for a workspace",
		Description: "Returns the workspace's auto-action rule rows (rule kind, optional signal_kind scope, condition, side-effect). On first read seeds the default wildcard rule set so admins can begin tweaking immediately.",
		Tags:        []string{"AI"},
	}, GetAutoActionRules(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-auto-action-rules-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/ai/auto-action-rules",
		Summary:     "Patch auto-action rules for a workspace",
		Description: "Updates one or more auto-action rules in a single request. Each row may toggle enabled, change its threshold, narrow its signal_kind scope, or rewrite its condition. Returns the post-update set so the UI can reconcile state.",
		Tags:        []string{"AI"},
	}, PatchAutoActionRules(deps))
}

// --- helpers ---

// seedDefaultRules inserts the 4 default wildcard auto-action rules
// for a workspace. The upsert uses INSERT IGNORE semantics so existing
// rows are not overwritten. Defaults always seed with NULL signal_kind
// — workspace admins create scoped rules explicitly via PATCH.
// AutonomyLevel is left at the zero value (NULL) so seeded rows fall
// back to confidence-based derivation; an operator must opt into an
// explicit level via the matrix UI.
func seedDefaultRules(ctx context.Context, q *generated.Queries, wsID uint32) error {
	for _, d := range defaultRules {
		if err := q.UpsertAutoActionRule(ctx, generated.UpsertAutoActionRuleParams{
			PublicID:      types.New(),
			WorkspaceID:   wsID,
			Kind:          d.kind,
			SignalKind:    sql.NullString{},
			Enabled:       d.enabled,
			Confidence:    d.confidence,
			IdleHours:     d.idleHours,
			AutonomyLevel: generated.NullAutoActionRulesAutonomyLevel{},
		}); err != nil {
			return err
		}
	}
	return nil
}

// hasAllDefaults reports whether the workspace already has a
// wildcard (NULL signal_kind) row for every default kind. Used after
// a PATCH to decide whether the seeding pass needs to run again.
func hasAllDefaults(rows []generated.ListAutoActionRulesForWorkspaceRow) bool {
	wildcardByKind := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if !r.SignalKind.Valid {
			wildcardByKind[r.Kind] = struct{}{}
		}
	}
	for _, d := range defaultRules {
		if _, ok := wildcardByKind[d.kind]; !ok {
			return false
		}
	}
	return true
}

// resolveRuleParams builds the upsert params for a single patch item by
// merging the patch fields with the existing row (looked up by exact
// (kind, signal_kind) address) or the kind's seed defaults.
func resolveRuleParams(wsID uint32, item PatchAutoActionRuleItem, existing map[ruleKey]generated.ListAutoActionRulesForWorkspaceRow) generated.UpsertAutoActionRuleParams {
	signalKind := sql.NullString{}
	if item.SignalKind != nil {
		signalKind = sql.NullString{String: *item.SignalKind, Valid: true}
	}

	params := generated.UpsertAutoActionRuleParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		Kind:        item.Kind,
		SignalKind:  signalKind,
	}

	// Start from the exact (kind, signal_kind) row when present so we
	// preserve its public_id and unchanged columns. A non-wildcard
	// upsert that has no existing row falls back to the kind's seed
	// defaults rather than reading the wildcard row — different
	// signal_kind means different row.
	if row, ok := existing[ruleKey{Kind: item.Kind, SignalKind: signalKind}]; ok {
		params.PublicID = row.PublicID
		params.Enabled = row.Enabled
		params.Confidence = row.Confidence
		params.IdleHours = row.IdleHours
		params.AutonomyLevel = row.AutonomyLevel
	} else {
		def := findDefault(item.Kind)
		params.Enabled = def.enabled
		params.Confidence = def.confidence
		params.IdleHours = def.idleHours
		// Seed-default rows ship with NULL autonomy_level so the
		// resolver falls back to confidence-based derivation until an
		// operator picks an explicit level via the matrix.
		params.AutonomyLevel = generated.NullAutoActionRulesAutonomyLevel{}
	}

	// Apply patch fields.
	if item.Enabled != nil {
		params.Enabled = *item.Enabled
	}
	if item.Confidence != nil {
		params.Confidence = fmt.Sprintf("%.2f", *item.Confidence)
	}
	if item.IdleHours != nil {
		params.IdleHours = uint32(*item.IdleHours) //#nosec G115 -- IdleHours is request-validated to maximum:8760 (one year)
	}
	if item.AutonomyLevel != nil {
		// Huma's enum tag on PatchAutoActionRuleItem.AutonomyLevel
		// rejects any non-enum string at the request boundary, so by
		// the time we land here *item.AutonomyLevel is guaranteed to
		// be one of suggest/draft/auto.
		params.AutonomyLevel = generated.NullAutoActionRulesAutonomyLevel{
			AutoActionRulesAutonomyLevel: generated.AutoActionRulesAutonomyLevel(*item.AutonomyLevel),
			Valid:                        true,
		}
	}

	return params
}

// findDefault returns the default seed values for a given rule kind.
func findDefault(kind string) ruleDefault {
	for _, d := range defaultRules {
		if d.kind == kind {
			return d
		}
	}
	// Fallback — should never happen given enum validation.
	return ruleDefault{kind: kind, enabled: true, confidence: "0.70", idleHours: 0}
}

// mapRuleRows converts a slice of auto_action_rules DB rows into the
// API response DTOs, converting the DECIMAL confidence string to
// float64 and surfacing signal_kind / autonomy_level as nullable
// strings (nil = NULL in DB, i.e. wildcard / no override).
func mapRuleRows(rows []generated.ListAutoActionRulesForWorkspaceRow) []AutoActionRuleBody {
	out := make([]AutoActionRuleBody, len(rows))
	for i, r := range rows {
		var signalKind *string
		if r.SignalKind.Valid {
			v := r.SignalKind.String
			signalKind = &v
		}
		var autonomyLevel *string
		if r.AutonomyLevel.Valid {
			v := string(r.AutonomyLevel.AutoActionRulesAutonomyLevel)
			autonomyLevel = &v
		}
		out[i] = AutoActionRuleBody{
			Kind:          r.Kind,
			SignalKind:    signalKind,
			Enabled:       r.Enabled,
			Confidence:    parseThreshold(r.Confidence),
			IdleHours:     int(r.IdleHours),
			AutonomyLevel: autonomyLevel,
		}
	}
	return out
}
