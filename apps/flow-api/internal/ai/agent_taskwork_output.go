package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

// taskWorkToolFinish ends a run. It is not a grant: every run may finish.
const taskWorkToolFinish = "finish"

// taskWorkOutput is the one move a task-work step returns. Every field is
// required and unused arguments are null, so the schema fits strict
// structured-output modes.
var taskWorkOutput = mustOutputSpec("agent_task_step", `{
	"$schema":"https://json-schema.org/draft/2020-12/schema",
	"type":"object",
	"properties":{
		"thought":{"type":"string","maxLength":500},
		"tool":{"type":"string","enum":["add_comment","set_priority","set_due_on","transition_task","create_subtask","finish"]},
		"args":{"type":"object",
			"properties":{
				"body":{"type":["string","null"]},
				"priority":{"type":["integer","null"]},
				"due_on":{"type":["string","null"]},
				"transition":{"type":["string","null"],"enum":["start","block","unblock","submit","complete",null]},
				"title":{"type":["string","null"]},
				"summary":{"type":["string","null"]}
			},
			"required":["body","priority","due_on","transition","title","summary"],
			"additionalProperties":false
		}
	},
	"required":["thought","tool","args"],
	"additionalProperties":false
}`)

// taskWorkRepairReminder is appended to the user turn of the one repair
// retry a step gets after an unusable answer.
const taskWorkRepairReminder = "Your previous response was not a JSON object matching the required schema. Respond with that JSON object only: no prose, no markdown fences."

// taskWorkArgs are a move's arguments. Priority stays a json.Number so the
// conversion to int32 can be strict.
type taskWorkArgs struct {
	Body       *string      `json:"body"`
	Priority   *json.Number `json:"priority"`
	DueOn      *string      `json:"due_on"`
	Transition *string      `json:"transition"`
	Title      *string      `json:"title"`
	Summary    *string      `json:"summary"`
}

// taskWorkMove is one validated, normalized model answer.
type taskWorkMove struct {
	Thought string       `json:"thought"`
	Tool    string       `json:"tool"`
	Args    taskWorkArgs `json:"args"`
}

// parseTaskWorkMove validates a response against taskWorkOutput and keeps
// only the arguments the chosen tool reads.
func parseTaskWorkMove(resp *providers.Response) (taskWorkMove, error) {
	payload, err := outputPayload(resp, taskWorkOutput)
	if err != nil {
		return taskWorkMove{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var m taskWorkMove
	if err := dec.Decode(&m); err != nil {
		return taskWorkMove{}, ErrParse
	}
	return m.normalized(), nil
}

// isTaskWorkOutputFailure reports whether a failed call produced an answer
// the model can be asked to repair, as opposed to a provider failure.
func isTaskWorkOutputFailure(err error) bool {
	return errors.Is(err, ErrParse) ||
		errors.Is(err, providers.ErrResponseSchemaMismatch) ||
		errors.Is(err, providers.ErrGenerationIncomplete)
}

func (m taskWorkMove) normalized() taskWorkMove {
	a := m.Args
	out := taskWorkMove{Thought: m.Thought, Tool: m.Tool}
	switch m.Tool {
	case execution.ToolAddComment:
		out.Args.Body = a.Body
	case execution.ToolSetPriority:
		out.Args.Priority = a.Priority
	case execution.ToolSetDueOn:
		out.Args.DueOn = a.DueOn
	case execution.ToolTransitionTask:
		out.Args.Transition = a.Transition
	case execution.ToolCreateSubtask:
		out.Args.Title = a.Title
	case taskWorkToolFinish:
		out.Args.Summary = a.Summary
	}
	return out
}

// sameAs reports whether two moves are the same: one tool, equal
// normalized arguments.
func (m taskWorkMove) sameAs(o taskWorkMove) bool {
	if m.Tool != o.Tool {
		return false
	}
	a, errA := json.Marshal(m.Args)
	b, errB := json.Marshal(o.Args)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

// action converts the move into a taskservice action. ok is false when an
// argument cannot be converted (a null or out-of-range priority, a due date
// that is not a calendar date); taskservice validates everything else.
func (m taskWorkMove) action(projectID uint32) (taskservice.Action, bool) {
	a := taskservice.Action{Kind: execution.Tool(m.Tool)}
	switch m.Tool {
	case execution.ToolAddComment:
		a.Body = deref(m.Args.Body)
	case execution.ToolSetPriority:
		if m.Args.Priority == nil {
			return a, false
		}
		p, err := strconv.ParseInt(string(*m.Args.Priority), 10, 32)
		if err != nil {
			return a, false
		}
		a.Priority = int32(p)
	case execution.ToolSetDueOn:
		if m.Args.DueOn != nil {
			d, err := time.Parse(time.DateOnly, *m.Args.DueOn)
			if err != nil {
				return a, false
			}
			a.DueOn.Time, a.DueOn.Valid = d, true
		}
	case execution.ToolTransitionTask:
		a.Transition = deref(m.Args.Transition)
	case execution.ToolCreateSubtask:
		a.Title = deref(m.Args.Title)
		a.ProjectID = projectID
	}
	return a, true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
