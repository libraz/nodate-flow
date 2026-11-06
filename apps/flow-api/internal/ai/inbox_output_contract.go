package ai

import (
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

var inboxTriageOutput = mustOutputSpec("inbox_triage", `{
	"$schema":"https://json-schema.org/draft/2020-12/schema",
	"type":"array",
	"items":{"$ref":"#/$defs/suggestion"},
	"$defs":{"suggestion":{"type":"object",
		"properties":{
			"inboxItemId":{"type":"string","minLength":1,"maxLength":36},
			"score":{"type":"number","minimum":0,"maximum":1},
			"recommendedAction":{"type":"string","enum":["archive","snooze","open"]},
			"reasoning":{"type":"string","maxLength":1000}
		},
		"required":["inboxItemId","score","recommendedAction","reasoning"],
		"additionalProperties":false
	}}
}`)

// validateInboxTriageOutput validates the provider's decoded content against
// the stable response contract, then confirms that every recommendation refers
// to a distinct item in the exact input snapshot authorized for this request.
func validateInboxTriageOutput(resp *providers.Response, sourceVersions map[string]string, openableInboxItems map[string]struct{}) ([]InboxTriageSuggestion, error) {
	payload, err := outputPayload(resp, inboxTriageOutput)
	if err != nil {
		return nil, err
	}
	suggestions, err := parseInboxTriageSuggestions(string(payload))
	if err != nil {
		return nil, err
	}
	proposed := make(map[string]struct{}, len(suggestions))
	for i := range suggestions {
		itemID, err := types.Parse(suggestions[i].InboxItemID)
		if err != nil {
			return nil, fmt.Errorf("%w: suggestion references an invalid inbox item ID", providers.ErrResponseSchemaMismatch)
		}
		canonicalID := itemID.String()
		if _, captured := sourceVersions[canonicalID]; !captured {
			return nil, fmt.Errorf("%w: suggestion references an uncaptured inbox item", providers.ErrResponseSchemaMismatch)
		}
		if _, duplicate := proposed[canonicalID]; duplicate {
			return nil, fmt.Errorf("%w: more than one suggestion references the same inbox item", providers.ErrResponseSchemaMismatch)
		}
		proposed[canonicalID] = struct{}{}
		suggestions[i].InboxItemID = canonicalID
		if suggestions[i].Score < 0 || suggestions[i].Score > 1 {
			return nil, fmt.Errorf("%w: suggestion score is outside the supported range", providers.ErrResponseSchemaMismatch)
		}
		switch suggestions[i].RecommendedAction {
		case "archive", "snooze", "open":
		default:
			return nil, fmt.Errorf("%w: suggestion action is outside the supported vocabulary", providers.ErrResponseSchemaMismatch)
		}
		if suggestions[i].RecommendedAction == "open" {
			if _, canOpen := openableInboxItems[canonicalID]; !canOpen {
				return nil, fmt.Errorf("%w: open action requires a captured readable task link", providers.ErrResponseSchemaMismatch)
			}
		}
	}
	return suggestions, nil
}
