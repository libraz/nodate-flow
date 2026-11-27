package taskservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
)

const dateLayout = "2006-01-02"

// ProposalArgs is the stored arguments of a proposal. Only the fields of
// its kind are set; a set_due_on proposal with DueOn nil clears the date.
type ProposalArgs struct {
	Body       string  `json:"body,omitempty"`
	Priority   *int32  `json:"priority,omitempty"`
	DueOn      *string `json:"dueOn,omitempty"`
	Transition string  `json:"transition,omitempty"`
	Title      string  `json:"title,omitempty"`
}

// ProposalBefore is the task value a proposal was made against, for the
// kinds that store one.
type ProposalBefore struct {
	Priority          *int32  `json:"priority,omitempty"`
	DueOn             *string `json:"dueOn,omitempty"`
	DerivedState      string  `json:"derivedState,omitempty"`
	TransitionEventID int64   `json:"transitionEventId,omitempty"`
}

func optionalDate(v sql.NullTime) any {
	if !v.Valid {
		return nil
	}
	return dateOnly(v)
}

// encodeProposalArgs normalizes an action into the stored args_json. The
// key order is fixed by encoding/json, so equal actions encode equally.
func encodeProposalArgs(a Action) (json.RawMessage, error) {
	var v map[string]any
	switch a.Kind {
	case execution.ToolAddComment:
		v = map[string]any{"body": a.Body}
	case execution.ToolSetPriority:
		v = map[string]any{"priority": a.Priority}
	case execution.ToolSetDueOn:
		v = map[string]any{"dueOn": optionalDate(a.DueOn)}
	case execution.ToolTransitionTask:
		v = map[string]any{"transition": a.Transition}
	case execution.ToolCreateSubtask:
		v = map[string]any{"title": a.Title}
	default:
		return nil, fmt.Errorf("taskservice: no proposal encoding for %q", a.Kind)
	}
	return json.Marshal(v)
}

// decodeProposalArgs turns stored args back into the action they encode.
func decodeProposalArgs(kind generated.AgentActionProposalsKind, raw json.RawMessage) (Action, ProposalArgs, error) {
	var args ProposalArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Action{}, ProposalArgs{}, fmt.Errorf("taskservice: decode proposal args: %w", err)
	}
	a := Action{Kind: execution.Tool(kind), Body: args.Body, Transition: args.Transition, Title: args.Title}
	if args.Priority != nil {
		a.Priority = *args.Priority
	}
	if args.DueOn != nil {
		due, err := parseDate(*args.DueOn)
		if err != nil {
			return Action{}, ProposalArgs{}, err
		}
		a.DueOn = due
	}
	return a, args, nil
}

func parseDate(s string) (sql.NullTime, error) {
	t, err := time.ParseInLocation(dateLayout, s, time.UTC)
	if err != nil {
		return sql.NullTime{}, fmt.Errorf("taskservice: stored date %q: %w", s, err)
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// preconditionFor records the value an approval is checked against; an
// empty object for the kinds whose staleness is the task's workability
// alone, because the generated row types cannot scan a NULL JSON column.
func (s *Service) preconditionFor(ctx context.Context, qtx *generated.Queries, ws, taskID uint32, current generated.FindTaskByPublicIdRow, kind execution.Tool) (json.RawMessage, error) {
	var v map[string]any
	switch kind {
	case execution.ToolSetPriority:
		v = map[string]any{"priority": current.Priority}
	case execution.ToolSetDueOn:
		v = map[string]any{"dueOn": optionalDate(current.DueOn)}
	case execution.ToolTransitionTask:
		id, err := latestTransitionEventID(ctx, qtx, ws, taskID)
		if err != nil {
			return nil, err
		}
		v = map[string]any{"derivedState": string(current.DerivedState), "transitionEventId": id}
	default:
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(v)
}

// storedPrecondition is a decoded precondition_json. set_due_on stores an
// explicit null for "no due date", which the pointer alone cannot carry.
type storedPrecondition struct {
	before   ProposalBefore
	hasDueOn bool
}

func decodePrecondition(raw json.RawMessage) (storedPrecondition, error) {
	var out storedPrecondition
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out.before); err != nil {
		return out, fmt.Errorf("taskservice: decode proposal precondition: %w", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return out, fmt.Errorf("taskservice: decode proposal precondition: %w", err)
	}
	_, out.hasDueOn = keys["dueOn"]
	return out, nil
}

// expectationFor builds the kit-level check an approval applies against.
func (p storedPrecondition) expectationFor(kind execution.Tool) (expectation, error) {
	var e expectation
	switch kind {
	case execution.ToolSetPriority:
		e.Priority = p.before.Priority
	case execution.ToolSetDueOn:
		if p.hasDueOn {
			due := sql.NullTime{}
			if p.before.DueOn != nil {
				var err error
				if due, err = parseDate(*p.before.DueOn); err != nil {
					return e, err
				}
			}
			e.DueOn = &due
		}
	case execution.ToolTransitionTask:
		if p.before.DerivedState != "" {
			state := generated.TasksDerivedState(p.before.DerivedState)
			e.State = &state
		}
	}
	return e, nil
}
