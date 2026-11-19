package handlerutil

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// IdempotencyKeyInput adds the optional key used to safely retry one
// mutation request. Embed it in Huma input structs for operations that
// persist an operation receipt.
type IdempotencyKeyInput struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"128" pattern:"^[A-Za-z0-9._~-]{1,128}$" doc:"Optional key for safely retrying the same request"`
}

// Resolve rejects explicitly blank and repeated values. The field's Huma
// pattern tag validates the single value's character set and length.
func (in *IdempotencyKeyInput) Resolve(ctx huma.Context) []error {
	var values []string
	ctx.EachHeader(func(name, value string) {
		if strings.EqualFold(name, "Idempotency-Key") {
			values = append(values, value)
		}
	})
	if len(values) > 1 {
		return []error{&huma.ErrorDetail{
			Message:  "header must be supplied at most once",
			Location: "header.Idempotency-Key",
		}}
	}
	if len(values) == 1 && strings.TrimSpace(values[0]) == "" {
		return []error{&huma.ErrorDetail{
			Message:  "header must not be blank",
			Location: "header.Idempotency-Key",
			Value:    values[0],
		}}
	}
	return nil
}
