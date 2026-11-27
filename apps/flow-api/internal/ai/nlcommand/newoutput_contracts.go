package nlcommand

import (
	"errors"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/outputcontract"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

var commandOutputSpec = mustCommandOutputSpec()

func mustCommandOutputSpec() *providers.OutputSpec {
	const schema = `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"properties":{
			"tool":{"type":"string"},
			"args":{"type":"object","additionalProperties":true},
			"confidence":{"type":"number"}
		},
		"required":["tool","confidence"],"additionalProperties":false
	}`
	spec, err := providers.NewOutputSpec("command_resolution", []byte(schema))
	if err != nil {
		panic("nlcommand: invalid static command output contract")
	}
	return spec
}

func validateCommandOutput(response *providers.Response) ([]byte, error) {
	if response == nil {
		return nil, errors.Join(ErrUnresolvable, providers.ErrResponseSchemaMismatch)
	}
	payload := []byte(strings.TrimSpace(response.Text))
	if err := outputcontract.Validate(commandOutputSpec, payload); err != nil {
		return nil, commandContractError(err)
	}
	// A no-match answer is a valid model output, recorded as a success.
	if _, err := ValidateBytes(payload, allowedTools); err != nil && !errors.Is(err, ErrNoMatch) {
		return nil, errors.Join(ErrUnresolvable, providers.ErrResponseSchemaMismatch)
	}
	return payload, nil
}

func commandContractError(err error) error {
	switch {
	case errors.Is(err, outputcontract.ErrInvalidJSON):
		return errors.Join(ErrUnresolvable, providers.ErrResponseInvalidJSON)
	case errors.Is(err, outputcontract.ErrValidationFailed):
		return errors.Join(ErrUnresolvable, providers.ErrResponseSchemaMismatch)
	default:
		return errors.Join(ErrUnresolvable, err)
	}
}
