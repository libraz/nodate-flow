package handlerutil

import (
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
)

type idempotencyHeader struct {
	name  string
	value string
}

type headerContextBase interface {
	huma.Context
}

type idempotencyHeaderContext struct {
	headerContextBase
	headers []idempotencyHeader
}

func (c idempotencyHeaderContext) EachHeader(visit func(name, value string)) {
	for _, header := range c.headers {
		visit(header.name, header.value)
	}
}

func TestIdempotencyKeyInputRejectsBlankAndRepeatedHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		headers  []idempotencyHeader
		wantErrs int
	}{
		{name: "absent is optional"},
		{name: "one key is accepted", headers: []idempotencyHeader{{name: "Idempotency-Key", value: "retry.1"}}},
		{name: "blank key", headers: []idempotencyHeader{{name: "Idempotency-Key", value: " "}}, wantErrs: 1},
		{name: "repeated key", headers: []idempotencyHeader{
			{name: "Idempotency-Key", value: "first"},
			{name: "idempotency-key", value: "second"},
		}, wantErrs: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := IdempotencyKeyInput{}
			errs := input.Resolve(idempotencyHeaderContext{headers: test.headers})
			require.Len(t, errs, test.wantErrs)
			if test.wantErrs != 0 {
				detail, ok := errs[0].(*huma.ErrorDetail)
				require.True(t, ok)
				require.Equal(t, "header.Idempotency-Key", detail.Location)
			}
		})
	}
}
