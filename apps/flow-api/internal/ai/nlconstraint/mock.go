package nlconstraint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// MockProvider is the deterministic fixture-backed Provider used
// under NF_AI_MOCK=1. It looks up the normalized prompt in
// testdata/ai/nl_constraint.json and returns the stored JSON bytes.
// Missing keys return ErrUnparseable so tests can cover both happy
// and sad paths without a network call.
type MockProvider struct {
	once   sync.Once
	loaded map[string]json.RawMessage
	err    error
}

// NewMockProvider constructs a MockProvider; the fixture is lazy-
// loaded on first call.
func NewMockProvider() *MockProvider { return &MockProvider{} }

// CompileConstraint implements Provider.
func (m *MockProvider) CompileConstraint(_ context.Context, prompt string) ([]byte, error) {
	m.once.Do(m.load)
	if m.err != nil {
		return nil, m.err
	}
	raw, ok := m.loaded[Normalize(prompt)]
	if !ok {
		return nil, ErrUnparseable
	}
	return []byte(raw), nil
}

func (m *MockProvider) load() {
	path, err := fixturePath()
	if err != nil {
		m.err = err
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		m.err = fmt.Errorf("read nl_constraint fixture: %w", err)
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		m.err = fmt.Errorf("decode nl_constraint fixture: %w", err)
		return
	}
	m.loaded = make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		m.loaded[Normalize(k)] = v
	}
}

func fixturePath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("nlconstraint: cannot resolve caller")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "testdata", "ai", "nl_constraint.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("nlconstraint: fixture nl_constraint.json not found")
}
