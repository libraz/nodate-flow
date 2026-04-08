package providers

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotImplemented is returned by stub providers until the concrete
// implementations land in 1.AI-1.
var ErrNotImplemented = errors.New("ai/providers: not implemented")

// Factory builds a Provider for a given encrypted-key context. The
// concrete signature will grow with the first real implementation; for
// the stub it just takes a kind so registry.go compiles.
type Factory func(kind Kind) Provider

// Registry maps a Kind to its factory. Lookup is the only public way to
// obtain a Provider so callers cannot accidentally instantiate one
// outside this package.
var registry = map[Kind]Factory{
	KindAnthropic:    func(k Kind) Provider { return &stub{kind: k} },
	KindOpenAI:       func(k Kind) Provider { return &stub{kind: k} },
	KindGoogle:       func(k Kind) Provider { return &stub{kind: k} },
	KindOllama:       func(k Kind) Provider { return &stub{kind: k} },
	KindOpenAICompat: func(k Kind) Provider { return &stub{kind: k} },
}

// Lookup returns the factory for kind, or false if the kind is unknown.
func Lookup(kind Kind) (Factory, bool) {
	f, ok := registry[kind]
	return f, ok
}

// stub is a placeholder Provider implementation that returns
// ErrNotImplemented for every call. The real Anthropic / OpenAI / Google /
// Ollama / openai_compat implementations replace it in 1.AI-1.
type stub struct {
	kind Kind
}

func (s *stub) Name() string {
	return fmt.Sprintf("stub:%s", s.kind)
}

func (s *stub) Generate(_ context.Context, _ Request) (*Response, error) {
	return nil, ErrNotImplemented
}
