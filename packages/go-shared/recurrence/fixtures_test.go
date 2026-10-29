package recurrence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sql/core/recurrence/fixtures.json is the core schema contract's own
// statement of what a rule expands to (PROTOCOL.md §Recurrence). Every
// application sharing the database runs it, so this expander and the
// other product's agree on the series a stored row names.

// coreFixture is one case of the core contract's fixture file.
type coreFixture struct {
	Name    string          `json:"name"`
	Rule    json.RawMessage `json:"rule"`
	DTStart string          `json:"dtstart"`
	TZ      string          `json:"tz"`
	Expect  []string        `json:"expect"`
}

func loadCoreFixtures(t *testing.T) []coreFixture {
	t.Helper()
	path := filepath.Join(repoRoot(t), "sql", "core", "recurrence", "fixtures.json")
	raw, err := os.ReadFile(path) //#nosec G304 -- repository path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []coreFixture
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no cases", path)
	}
	return out
}

// TestFixturesExpandAsTheCoreContractStates asserts every case exactly:
// the instants and the offsets they are written with. A bounded rule
// (count or until) has to stop where expect stops; an unbounded one has
// to open with expect.
func TestFixturesExpandAsTheCoreContractStates(t *testing.T) {
	t.Parallel()
	for _, fx := range loadCoreFixtures(t) {
		t.Run(fx.Name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateRule(fx.Rule); err != nil {
				t.Fatalf("ValidateRule(%s) = %v, want nil", fx.Rule, err)
			}
			rule, err := ParseRule(fx.Rule)
			if err != nil || rule == nil {
				t.Fatalf("ParseRule(%s) = %v, %v", fx.Rule, rule, err)
			}
			start := mustParseTime(t, "dtstart", fx.DTStart)
			evt := Event{StartAt: start, EndAt: start.Add(time.Hour), Timezone: fx.TZ, Rule: rule}

			bounded := rule.Count != nil || rule.Until != ""
			n := len(fx.Expect)
			if bounded {
				n += 10
			}
			got := Starts(evt, start, n, time.Time{})
			if !bounded && len(got) > len(fx.Expect) {
				got = got[:len(fx.Expect)]
			}

			formatted := make([]string, 0, len(got))
			for _, s := range got {
				formatted = append(formatted, s.Format(time.RFC3339))
			}
			if len(formatted) != len(fx.Expect) {
				t.Fatalf("occurrences = %v, want %v", formatted, fx.Expect)
			}
			for i := range formatted {
				if formatted[i] != fx.Expect[i] {
					t.Errorf("occurrence %d = %s, want %s", i, formatted[i], fx.Expect[i])
				}
			}
		})
	}
}
