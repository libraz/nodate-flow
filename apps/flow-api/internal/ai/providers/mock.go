// Package providers - mock.go is a deterministic in-memory Provider used
// when NF_AI_MOCK=1. It bypasses all upstream HTTP calls and returns
// fixture text loaded from apps/flow-api/testdata/ai/. The mock is the seam
// tests rely on for reproducible LLM behaviour.
package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MockKind is the synthetic Kind tag used by the mock provider so logs
// and audit records can distinguish it from real upstream providers.
const MockKind Kind = "mock"

// MockProvider is a deterministic Provider that ignores Request.Prompt
// and returns canned text loaded from a fixture file. It is intended for
// the NF_AI_MOCK=1 development / test path only.
type MockProvider struct {
	// FixtureDir is the directory under which fixture JSON files live.
	// Empty means "use the default search path" (see resolveFixtureDir).
	FixtureDir string

	mu    sync.Mutex
	cache map[string]string
}

// NewMockProvider returns a MockProvider rooted at fixtureDir. Pass an
// empty string to fall back to the default search path.
func NewMockProvider(fixtureDir string) *MockProvider {
	return &MockProvider{FixtureDir: fixtureDir, cache: map[string]string{}}
}

// Name implements Provider.
func (m *MockProvider) Name() string { return "mock" }

// Kind implements Provider.
func (m *MockProvider) Kind() Kind { return MockKind }

// Complete implements Provider. The mock ignores req.Prompt and returns
// the fixture [fixtureNameForSystem] selects for req.System, falling back
// to inbox_triage for any purpose that has no entry. The returned Response
// carries zero tokens / zero cost so the cost guard never trips in tests.
func (m *MockProvider) Complete(_ context.Context, req Request) (*Response, error) {
	name := fixtureNameForSystem(req.System)
	text, err := m.load(name)
	if err != nil {
		return nil, err
	}
	return &Response{Text: text}, nil
}

// LoadFixture returns the raw text of a named fixture (without the .json
// suffix). It is exported so the orchestrator can request a specific
// fixture by name when System-prompt routing is not enough.
func (m *MockProvider) LoadFixture(name string) (string, error) {
	return m.load(name)
}

func (m *MockProvider) load(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache == nil {
		m.cache = map[string]string{}
	}
	if cached, ok := m.cache[name]; ok {
		return cached, nil
	}
	dir, err := resolveFixtureDir(m.FixtureDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".json")
	b, err := os.ReadFile(path) //nolint:gosec // path composed from constant fixture name
	if err != nil {
		return "", fmt.Errorf("ai/providers: load fixture %q: %w", name, err)
	}
	m.cache[name] = string(b)
	return string(b), nil
}

// smartCreateMarker identifies the smart-create system prompt. It is the
// response key that prompt asks the model to emit, which makes it a
// property of the smart-create contract rather than of its prose: the
// wording can be rewritten without breaking the routing, and dropping the
// key would be a contract change that should break it.
//
// Matching a substring rather than comparing against the prompt constant
// keeps this package free of an import cycle — internal/ai imports
// providers, not the other way round. [TestMockFixtureRoutingForSmartCreate]
// in internal/ai asserts the two stay in agreement.
const smartCreateMarker = "suggestedAssignees"

// fixtureNameForSystem maps an orchestrator system prompt to a fixture
// file name.
//
// The mapping is deliberately incomplete. Only purposes whose fixture is
// actually asserted on get an entry; everything else falls through to
// inbox_triage, which is the historical default. That fallback is a known
// sharp edge — an unrouted purpose silently receives triage JSON and
// produces a confidently wrong proposal rather than an error — so adding a
// purpose here should come with a test that would fail if the routing
// regressed to the fallback.
func fixtureNameForSystem(system string) string {
	if strings.Contains(system, smartCreateMarker) {
		return "smart_create"
	}
	return "inbox_triage"
}

// resolveFixtureDir returns the directory used to load fixture files.
// When override is non-empty it is returned as-is. Otherwise the
// function walks upward from the current working directory looking for
// a "testdata/ai" directory under an "apps/flow-api" ancestor; this lets the
// mock work both from `cd apps/flow-api && go test` and from the repo root.
func resolveFixtureDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv("NF_FLOW_AI_MOCK_FIXTURE_DIR"); env != "" {
		return env, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "apps", "api", "testdata", "ai")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
		candidate = filepath.Join(dir, "testdata", "ai")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("ai/providers: could not locate testdata/ai fixture dir")
}
