package nlconstraint

import (
	"errors"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/outputcontract"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint"
)

var constraintOutputSpec = mustConstraintOutputSpec()

func mustConstraintOutputSpec() *providers.OutputSpec {
	const schema = `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"$ref":"#/$defs/constraint",
		"$defs":{"constraint":{
			"type":"object",
			"properties":{
				"op":{"type":"string"},
				"terms":{"type":"array","items":{"$ref":"#/$defs/constraint"}},
				"term":{"$ref":"#/$defs/constraint"},
				"arg":{"type":"string"},
				"taskIds":{"type":"array","items":{"type":"string"}},
				"max":{"type":"integer"}
			},
			"required":["op"],"additionalProperties":false
		}}
	}`
	spec, err := providers.NewOutputSpec("constraint_dsl", []byte(schema))
	if err != nil {
		panic("nlconstraint: invalid static constraint output contract")
	}
	return spec
}

func validateConstraintOutput(response *providers.Response) ([]byte, error) {
	if response == nil {
		return nil, errors.Join(ErrUnparseable, providers.ErrResponseSchemaMismatch)
	}
	payload := []byte(strings.TrimSpace(response.Text))
	if err := outputcontract.Validate(constraintOutputSpec, payload); err != nil {
		return nil, constraintContractError(err)
	}
	if _, err := constraint.Parse(payload); err != nil {
		return nil, errors.Join(ErrUnparseable, providers.ErrResponseSchemaMismatch)
	}
	return payload, nil
}

func constraintContractError(err error) error {
	switch {
	case errors.Is(err, outputcontract.ErrInvalidJSON):
		return errors.Join(ErrUnparseable, providers.ErrResponseInvalidJSON)
	case errors.Is(err, outputcontract.ErrValidationFailed):
		return errors.Join(ErrUnparseable, providers.ErrResponseSchemaMismatch)
	default:
		return errors.Join(ErrUnparseable, err)
	}
}
