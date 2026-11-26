// MCP tools over a calendar's labels. The rules (name, colour, cap, name
// uniqueness) live in calendarlabels and the role decision in the calendars
// package; what is left here is reading the arguments and recording the change
// inside the write transaction.

package mcp

import (
	"context"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// calendarLabelColorPattern is the #RRGGBB form the REST label inputs accept.
const calendarLabelColorPattern = `^#[0-9A-Fa-f]{6}$`

// calendarLabelOut is a label as the tools answer it: the fields of the REST
// label response, with the times as unix seconds.
func calendarLabelOut(l calendarlabels.Label) map[string]any {
	return map[string]any{
		"id":        l.PublicID.String(),
		"name":      l.Name,
		"color":     l.Color,
		"createdAt": l.CreatedAt.Unix(),
		"updatedAt": l.UpdatedAt.Unix(),
	}
}

// calendarLabelMutation is the record of one label change, the same one
// the REST handlers file.
func calendarLabelMutation(eventType sharedbus.Kind, action, callSite, calID string, l calendarlabels.Label) mutationlog.Mutation {
	return mutationlog.Mutation{
		EventType:       eventType,
		AuditAction:     action,
		ResourceType:    "calendar.label",
		ResourceID:      l.PublicID.String(),
		SubjectPublicID: l.PublicID,
		Payload: map[string]any{
			"calendarId": calID,
			"labelId":    l.PublicID.String(),
			"summary":    calendarlabels.Summary(l.Name),
		},
		CallSite: callSite,
	}
}

// recordCalendarLabelChange returns the write-transaction hook that files m
// under the calendar the label belongs to, so the record commits or rolls
// back with the label write.
func recordCalendarLabelChange(deps Deps, s *session, calendarID uint32, build func(calendarlabels.Label) mutationlog.Mutation) calendarlabels.AfterWrite {
	return func(ctx context.Context, tx *dbretry.Tx, l calendarlabels.Label) error {
		calID64 := int64(calendarID)
		m := build(l)
		m.CalendarID = &calID64
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), m)
	}
}

func runListCalendarLabels(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID string `json:"calendarId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendar(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	rows, err := deps.CalendarQueries.ListCalendarLabels(ctx, calID)
	if err != nil {
		return nil, apierrors.New(apierrors.CalendarLabelListQueryInterrupted)
	}
	labels := make([]map[string]any, len(rows))
	for i, r := range rows {
		labels[i] = calendarLabelOut(calendarlabels.FromListRow(r))
	}
	return map[string]any{"labels": labels}, nil
}

func runCreateCalendarLabel(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID string `json:"calendarId"`
		Name       string `json:"name"`
		Color      string `json:"color"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" || in.Name == "" || in.Color == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendarLabelWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	label, refusal := calendarlabels.Create(ctx, deps.DB, deps.CalendarQueries, s.workspaceID, calID, in.Name, in.Color,
		recordCalendarLabelChange(deps, s, calID, func(l calendarlabels.Label) mutationlog.Mutation {
			return calendarLabelMutation(sharedbus.CalLabelCreated, "calendar.label.create", "mcp.create_calendar_label", in.CalendarID, l)
		}))
	if refusal != nil {
		return nil, refusal
	}
	return calendarLabelOut(label), nil
}

func runUpdateCalendarLabel(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID string  `json:"calendarId"`
		LabelID    string  `json:"labelId"`
		Name       *string `json:"name"`
		Color      *string `json:"color"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" || in.LabelID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendarLabelWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	// A call that changes nothing writes and records nothing, as REST's does.
	label, _, refusal := calendarlabels.Update(ctx, deps.DB, deps.CalendarQueries, s.workspaceID, calID, in.LabelID, in.Name, in.Color,
		recordCalendarLabelChange(deps, s, calID, func(l calendarlabels.Label) mutationlog.Mutation {
			return calendarLabelMutation(sharedbus.CalLabelUpdated, "calendar.label.update", "mcp.update_calendar_label", in.CalendarID, l)
		}))
	if refusal != nil {
		return nil, refusal
	}
	return calendarLabelOut(label), nil
}

func runDeleteCalendarLabel(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID string `json:"calendarId"`
		LabelID    string `json:"labelId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" || in.LabelID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendarLabelWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	_, refusal := calendarlabels.Delete(ctx, deps.DB, deps.CalendarQueries, calID, in.LabelID,
		recordCalendarLabelChange(deps, s, calID, func(l calendarlabels.Label) mutationlog.Mutation {
			return calendarLabelMutation(sharedbus.CalLabelDeleted, "calendar.label.delete", "mcp.delete_calendar_label", in.CalendarID, l)
		}))
	if refusal != nil {
		return nil, refusal
	}
	return map[string]any{"success": true}, nil
}
