// Package signalwire is the single source of truth for the signal wire
// contract shared across services: the closed `signals.source` enum and
// the JSON request body of POST /signals.
//
// Every layer that previously hand-wrote the source enum (the flow-api
// Huma input tag, the DB ENUM, the signal_kinds registry, and the
// flow-worker / presence-discord HTTP clients) now derives its values
// from Sources here. A future mismatch becomes a build/test failure
// rather than a runtime 422: AssertSourcesCovered cross-checks any
// candidate source set (e.g. the signal_kinds registry) against this
// list, and flow-api's Huma enum tag is asserted against SourceEnumTag
// at init time.
package signalwire

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Source is a signals.source enum value (the originating channel of a
// signal). It is a string-backed type so callers can keep using bare
// string literals where convenient while still having a typed set.
type Source string

// Canonical source values. These mirror the `source ENUM(...)` in
// sql/tables/signals.sql exactly; the DB enum and this list must agree.
//
//   - Manual   — user-emitted from the UI or CLI.
//   - GitHub   — inbound GitHub webhook.
//   - Slack    — inbound Slack Events API.
//   - Email    — inbound email pipeline.
//   - Google   — inbound Google Drive / Calendar push notification.
//   - Webhook  — generic inbound webhook receiver.
//   - Calendar — internal scheduler tick (flow-worker calendar_event_day
//     job); not a user-facing webhook source.
//   - Discord  — presence-discord gateway.
const (
	SourceManual   Source = "manual"
	SourceGitHub   Source = "github"
	SourceSlack    Source = "slack"
	SourceEmail    Source = "email"
	SourceGoogle   Source = "google"
	SourceWebhook  Source = "webhook"
	SourceCalendar Source = "calendar"
	SourceDiscord  Source = "discord"
)

// sources is the ordered canonical list. Order is the wire-enum order in
// sql/tables/signals.sql; SourceEnumTag and the DB ENUM both depend on
// it, so keep new entries appended (do not reorder) to keep generated
// artefacts stable.
var sources = []Source{
	SourceManual,
	SourceGitHub,
	SourceSlack,
	SourceEmail,
	SourceGoogle,
	SourceWebhook,
	SourceCalendar,
	SourceDiscord,
}

// sourceSet is the membership index built once from sources.
var sourceSet = func() map[Source]struct{} {
	m := make(map[Source]struct{}, len(sources))
	for _, s := range sources {
		m[s] = struct{}{}
	}
	return m
}()

// Sources returns the canonical source list in wire-enum order. The
// returned slice is a copy; callers may sort or mutate it freely.
func Sources() []Source {
	out := make([]Source, len(sources))
	copy(out, sources)
	return out
}

// SourceStrings returns the canonical sources as plain strings, in
// wire-enum order. Handy for building enum tags / DB enum bodies.
func SourceStrings() []string {
	out := make([]string, len(sources))
	for i, s := range sources {
		out[i] = string(s)
	}
	return out
}

// IsSource reports whether s is a member of the canonical source set.
func IsSource(s string) bool {
	_, ok := sourceSet[Source(s)]
	return ok
}

// SourceEnumTag is the comma-joined source list in wire-enum order,
// suitable for a Huma `enum:"..."` struct tag value. The flow-api
// SignalCreateInputBody.Source tag must equal this string; an init-time
// assertion in that package fails the build if they drift.
func SourceEnumTag() string {
	parts := make([]string, len(sources))
	for i, s := range sources {
		parts[i] = string(s)
	}
	return strings.Join(parts, ",")
}

// AssertSourcesCovered verifies that every source in candidates is a
// member of the canonical wire enum. It returns an error naming the
// offending values so callers can surface it as a build-time panic or a
// failing test. candidates is typically the distinct set of `Source`
// values declared by the signal_kinds registry.
//
// The check is one-directional on purpose: every registry source MUST be
// a wire-enum member (else the signal it advertises would be rejected by
// Huma with a 422 before the handler runs — the B-1 class of bug). The
// reverse is intentionally NOT required: sources such as github, slack,
// google, email, and webhook are emitted by the chi-level webhook
// handlers with provider-specific kinds that never pass through the
// signal_kinds registry, so a wire-enum source with no registry kind is
// legitimate, not drift.
func AssertSourcesCovered(candidates []string) error {
	var unknown []string
	seen := map[string]struct{}{}
	for _, c := range candidates {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		if !IsSource(c) {
			unknown = append(unknown, c)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf(
		"signalwire: source(s) %v are not members of the canonical wire enum %v; "+
			"add them to packages/go-shared/signalwire + sql/tables/signals.sql or remove them from the emitter/registry",
		unknown, SourceStrings())
}

// CreateRequest is the JSON body of POST /signals — the single shared
// wire shape used by every signal emitter (flow-api Huma DTO,
// flow-worker, presence-discord). Field names and casing are the wire
// contract; do not change them without regenerating the OpenAPI/SDK.
//
// SubjectType / SubjectID address the signal's subject (ADR 0008 D1).
// TaskID is the legacy fast path equivalent to (SubjectType="task",
// SubjectID=<task public id>). All ids are UUID v7 public ids — internal
// numeric ids are never sent (CLAUDE.md rule 18).
//
// This struct carries no validation tags itself; flow-api's Huma input
// embeds it and supplies the `enum` / `required` / length tags so the
// OpenAPI document stays authoritative. Emitter-side callers (worker,
// discord) populate it directly and rely on flow-api's validation.
type CreateRequest struct {
	WorkspaceID string          `json:"workspaceId"`
	Source      string          `json:"source"`
	Kind        string          `json:"kind"`
	ExternalID  string          `json:"externalId,omitempty"`
	TaskID      string          `json:"taskId,omitempty"`
	SubjectType string          `json:"subjectType,omitempty"`
	SubjectID   string          `json:"subjectId,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ExpiresAt   *int64          `json:"expiresAt,omitempty"`
}
