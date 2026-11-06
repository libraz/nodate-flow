package ai

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/outputcontract"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

var (
	tasksFromOutput = mustOutputSpec("tasks_from", `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"array",
		"items":{"$ref":"#/$defs/task"},
		"$defs":{"task":{"type":"object",
			"properties":{
				"title":{"type":"string","minLength":1,"maxLength":500},
				"description":{"type":"string","maxLength":50000},
				"priority":{"type":"string","enum":["low","medium","high"]}
			},
			"required":["title","description","priority"],"additionalProperties":false
		}}
	}`)
	priorityOutput = mustOutputSpec("priority", `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"properties":{
			"priority":{"type":"string","enum":["low","medium","high"]},
			"reason":{"type":"string","maxLength":1000}
		},
		"required":["priority","reason"],"additionalProperties":false
	}`)
	stepsOutput = mustOutputSpec("steps", `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"array","minItems":1,"maxItems":15,
		"items":{"$ref":"#/$defs/task"},
		"$defs":{"task":{"type":"object",
			"properties":{
				"title":{"type":"string","minLength":1,"maxLength":500},
				"description":{"type":"string","maxLength":50000},
				"priority":{"type":"string","enum":["low","medium","high"]}
			},
			"required":["title","description","priority"],"additionalProperties":false
		}}
	}`)
	smartCreateOutput = mustOutputSpec("smart_create", `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"properties":{
			"suggestedAssignees":{"type":"array","maxItems":3,"items":{"$ref":"#/$defs/assignee"}},
			"subtasks":{"type":"array","minItems":2,"maxItems":7,"items":{"$ref":"#/$defs/subtask"}}
		},
		"required":["suggestedAssignees","subtasks"],"additionalProperties":false,
		"$defs":{
			"assignee":{"type":"object",
				"properties":{
					"userPublicId":{"type":"string","minLength":1,"maxLength":64},
					"displayName":{"type":"string","minLength":1,"maxLength":200},
					"confidence":{"type":"number","minimum":0,"maximum":1},
					"reason":{"type":"string","maxLength":1000}
				},
				"required":["userPublicId","displayName","confidence","reason"],"additionalProperties":false
			},
			"subtask":{"type":"object",
				"properties":{
					"title":{"type":"string","minLength":1,"maxLength":500},
					"description":{"type":"string","maxLength":50000},
					"priority":{"type":"string","enum":["low","medium","high"]},
					"assignee":{"$ref":"#/$defs/assignee"}
				},
				"required":["title","description","priority"],"additionalProperties":false
			}
		}
	}`)
)

func mustOutputSpec(contract, schema string) *providers.OutputSpec {
	spec, err := providers.NewOutputSpec(contract, []byte(schema))
	if err != nil {
		panic("ai: invalid built-in output contract " + contract)
	}
	return spec
}

// outputPayload extracts the supported JSON value/fence, then checks the
// exact payload against its server-owned contract before typed decoding.
func outputPayload(response *providers.Response, spec *providers.OutputSpec) ([]byte, error) {
	if response == nil {
		return nil, providers.ErrResponseSchemaMismatch
	}
	payload := extractJSON(response.Text)
	if payload == "" {
		return nil, ErrParse
	}
	if err := outputcontract.Validate(spec, []byte(payload)); err != nil {
		if errors.Is(err, outputcontract.ErrInvalidJSON) {
			return nil, ErrParse
		}
		if errors.Is(err, outputcontract.ErrValidationFailed) {
			return nil, fmt.Errorf("%w: output contract rejected the response", providers.ErrResponseSchemaMismatch)
		}
		return nil, fmt.Errorf("ai: output contract is unavailable")
	}
	return []byte(payload), nil
}

func validateProposedTasks(tasks []ProposedTask, minCount, maxCount int) error {
	if len(tasks) < minCount || len(tasks) > maxCount {
		return fmt.Errorf("%w: task count is outside the requested range", providers.ErrResponseSchemaMismatch)
	}
	return validateProposedTaskFields(tasks)
}

func validateProposedTaskFields(tasks []ProposedTask) error {
	for _, task := range tasks {
		if strings.TrimSpace(task.Title) == "" || utf8.RuneCountInString(task.Title) > 500 || utf8.RuneCountInString(task.Description) > 50000 {
			return fmt.Errorf("%w: task text is outside its allowed bounds", providers.ErrResponseSchemaMismatch)
		}
		if !validPriorityLabel(task.Priority) {
			return fmt.Errorf("%w: task priority is outside the supported vocabulary", providers.ErrResponseSchemaMismatch)
		}
	}
	return nil
}

func validPriorityLabel(priority string) bool {
	switch priority {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func validateSmartProposal(proposal *SmartProposal, members []smartCreateMember) error {
	if proposal == nil || len(proposal.SuggestedAssignees) > 3 || len(proposal.Subtasks) < 2 || len(proposal.Subtasks) > 7 {
		return fmt.Errorf("%w: smart-create proposal is outside its allowed bounds", providers.ErrResponseSchemaMismatch)
	}
	for _, suggestion := range proposal.SuggestedAssignees {
		if err := validateAssigneeSuggestion(suggestion, members); err != nil {
			return err
		}
	}
	for _, subtask := range proposal.Subtasks {
		if strings.TrimSpace(subtask.Title) == "" || utf8.RuneCountInString(subtask.Title) > 500 || utf8.RuneCountInString(subtask.Description) > 50000 || !validPriorityLabel(subtask.Priority) {
			return fmt.Errorf("%w: smart-create task is outside its allowed bounds", providers.ErrResponseSchemaMismatch)
		}
		if subtask.Assignee != nil {
			if err := validateAssigneeSuggestion(*subtask.Assignee, members); err != nil {
				return err
			}
		}
	}
	return nil
}

type smartCreateMember struct {
	publicID    string
	displayName string
}

func validateAssigneeSuggestion(suggestion AssigneeSuggestion, members []smartCreateMember) error {
	if suggestion.Confidence < 0 || suggestion.Confidence > 1 || strings.TrimSpace(suggestion.DisplayName) == "" || utf8.RuneCountInString(suggestion.DisplayName) > 200 || utf8.RuneCountInString(suggestion.Reason) > 1000 {
		return fmt.Errorf("%w: smart-create assignee is outside its allowed bounds", providers.ErrResponseSchemaMismatch)
	}
	for _, member := range members {
		if suggestion.UserPublicID == member.publicID && suggestion.DisplayName == member.displayName {
			return nil
		}
	}
	return fmt.Errorf("%w: smart-create assignee is not a current workspace member", providers.ErrResponseSchemaMismatch)
}

func smartCreateMembers(rows []generated.ListWorkspaceMembersForSmartCreateRow) []smartCreateMember {
	members := make([]smartCreateMember, 0, len(rows))
	for _, row := range rows {
		members = append(members, smartCreateMember{publicID: row.UserPublicID.String(), displayName: row.DisplayName})
	}
	return members
}
