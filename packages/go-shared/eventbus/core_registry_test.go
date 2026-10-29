package eventbus

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// registeredOnly are core kinds a reader must resolve but this module does
// not emit, so kinds.go declares no constant for them.
var registeredOnly = map[string]bool{
	"calendar.member.updated":         true,
	"calendar.event.attendee.updated": true,
}

type yamlKind struct {
	family  string
	subject string
}

// readCoreYAML reads only the `- kind:` entries and their scalar fields,
// the same subset every other tool reads, so no YAML parser is involved.
func readCoreYAML(t *testing.T) map[string]yamlKind {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "sql", "core", "event-kinds.yaml"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	kindRe := regexp.MustCompile(`^\s*- kind:\s*(\S+)\s*$`)
	fieldRe := regexp.MustCompile(`^\s+(family|subject):\s*(.*?)\s*$`)
	out := map[string]yamlKind{}
	var cur string
	for _, line := range strings.Split(string(raw), "\n") {
		if m := kindRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			out[cur] = yamlKind{}
			continue
		}
		if m := fieldRe.FindStringSubmatch(line); m != nil && cur != "" {
			e := out[cur]
			v := strings.Trim(m[2], `"`)
			if m[1] == "family" {
				e.family = v
			} else {
				e.subject = v
			}
			out[cur] = e
		}
	}
	if len(out) == 0 {
		t.Fatal("registry yaml holds no kinds; every check would pass on an empty set")
	}
	return out
}

// TestCoreRegistryMatchesYAML proves the hand-written table is the yaml:
// same kinds, same family, same subject, and every yaml kind is either a
// declared constant or an explicitly registered-only kind.
func TestCoreRegistryMatchesYAML(t *testing.T) {
	t.Parallel()

	yaml := readCoreYAML(t)
	constants := map[string]bool{}
	for _, v := range declaredKindConstants(t) {
		constants[v] = true
	}

	for kind, y := range yaml {
		info, ok := coreKinds[kind]
		if !ok {
			t.Errorf("%s is in the yaml but not in the Go table", kind)
			continue
		}
		if string(info.Family) != y.family {
			t.Errorf("%s: Go family %q, yaml %q", kind, info.Family, y.family)
		}
		if info.Subject != y.subject {
			t.Errorf("%s: Go subject %q, yaml %q", kind, info.Subject, y.subject)
		}
		if !constants[kind] && !registeredOnly[kind] {
			t.Errorf("%s has no Go constant and is not registered-only", kind)
		}
		if constants[kind] && registeredOnly[kind] {
			t.Errorf("%s now has a constant; drop it from registeredOnly", kind)
		}
	}
	for kind := range coreKinds {
		if _, ok := yaml[kind]; !ok {
			t.Errorf("%s is in the Go table but not in the yaml", kind)
		}
	}
}

// TestCoreRegistryResolvesFamilies proves each registered kind resolves to
// the family the yaml gives it, through both entry points.
func TestCoreRegistryResolvesFamilies(t *testing.T) {
	t.Parallel()

	for kind, y := range readCoreYAML(t) {
		got, ok := FamilyForEventType(kind)
		if !ok || string(got) != y.family {
			t.Errorf("FamilyForEventType(%q) = %q, %v; want %q", kind, got, ok, y.family)
		}
		if got, ok := FamilyOf(Kind(kind)); !ok || string(got) != y.family {
			t.Errorf("FamilyOf(%q) = %q, %v; want %q", kind, got, ok, y.family)
		}
		if SubjectOf(Kind(kind)) != y.subject {
			t.Errorf("SubjectOf(%q) = %q; want %q", kind, SubjectOf(Kind(kind)), y.subject)
		}
	}
}

// TestCoreRegistryCoversCalendarAndWorkspaceConstants proves no Go
// calendar.* or workspace.* kind is missing from the yaml.
func TestCoreRegistryCoversCalendarAndWorkspaceConstants(t *testing.T) {
	t.Parallel()

	yaml := readCoreYAML(t)
	for name, value := range declaredKindConstants(t) {
		if !strings.HasPrefix(value, "calendar.") && !strings.HasPrefix(value, "workspace.") {
			continue
		}
		if _, ok := yaml[value]; !ok {
			t.Errorf("%s = %q is a Go kind missing from sql/core/event-kinds.yaml", name, value)
		}
	}
}
