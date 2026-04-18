package nlcommand

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// MockProvider is the deterministic fixture-backed Provider used under
// NF_AI_MOCK=1. It looks up the normalized prompt in
// testdata/ai/nl_command.json and returns the stored ToolCall bytes.
// Missing keys return nlcommand.ErrUnresolvable so tests can cover both
// happy and sad paths without a network call.
type MockProvider struct {
	once   sync.Once
	loaded map[string]json.RawMessage
	err    error
}

// NewMockProvider constructs a MockProvider; the fixture is lazy-loaded
// on first call so the helper stays cheap for tests that never exercise
// NL command resolution.
func NewMockProvider() *MockProvider { return &MockProvider{} }

// ResolveCommand implements Provider.
func (m *MockProvider) ResolveCommand(_ context.Context, prompt string, _ []ToolSpec) ([]byte, error) {
	m.once.Do(m.load)
	if m.err != nil {
		return nil, m.err
	}
	key := Normalize(prompt)
	raw, ok := m.loaded[key]
	if !ok {
		return nil, ErrUnresolvable
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
		m.err = fmt.Errorf("read nl_command fixture: %w", err)
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		m.err = fmt.Errorf("decode nl_command fixture: %w", err)
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
		return "", fmt.Errorf("nlcommand: cannot resolve caller")
	}
	// internal/ai/nlcommand/mock.go -> walk up to apps/flow-api/ then into testdata.
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "testdata", "ai", "nl_command.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("nlcommand: fixture nl_command.json not found")
}
