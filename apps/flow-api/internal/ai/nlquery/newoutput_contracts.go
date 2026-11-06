package nlquery

import (
	"errors"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/outputcontract"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

var lensOutputSpec = mustLensOutputSpec()

func mustLensOutputSpec() *providers.OutputSpec {
	const schema = `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"properties":{
			"filter":{"type":"object","minProperties":1,"additionalProperties":{"type":"object","minProperties":1,"additionalProperties":{}}},
			"sort":{"type":"array","items":{"type":"object","properties":{"field":{"type":"string"},"dir":{"type":"string"}},"required":["field","dir"],"additionalProperties":false}},
			"groupBy":{"type":["string","null"]}
		},
		"required":["filter"],"additionalProperties":false
	}`
	spec, err := providers.NewOutputSpec("lens", []byte(schema))
	if err != nil {
		panic("nlquery: invalid static Lens output contract")
	}
	return spec
}

func validateLensOutput(response *providers.Response) ([]byte, error) {
	if response == nil {
		return nil, errors.Join(ErrUnparseable, providers.ErrResponseSchemaMismatch)
	}
	payload := []byte(strings.TrimSpace(response.Text))
	if err := outputcontract.Validate(lensOutputSpec, payload); err != nil {
		return nil, lensContractError(err)
	}
	if _, err := ValidateBytes(payload); err != nil {
		return nil, errors.Join(ErrUnparseable, providers.ErrResponseSchemaMismatch)
	}
	return payload, nil
}

func lensContractError(err error) error {
	switch {
	case errors.Is(err, outputcontract.ErrInvalidJSON):
		return errors.Join(ErrUnparseable, providers.ErrResponseInvalidJSON)
	case errors.Is(err, outputcontract.ErrValidationFailed):
		return errors.Join(ErrUnparseable, providers.ErrResponseSchemaMismatch)
	default:
		return errors.Join(ErrUnparseable, err)
	}
}
