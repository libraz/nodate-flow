package config

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// composeFile is the production stack, relative to this package.
var composeFile = filepath.Join("..", "..", "..", "..", "compose.prod.yml")

// composeService is the compose service that runs this binary.
const composeService = "flow-worker"

// notForwarded are the struct variables compose.prod.yml deliberately leaves
// out of the service environment, each with the reason.
var notForwarded = map[string]string{}

// divergentDefaults are the variables whose compose value deliberately
// differs from the envDefault applied when unset.
var divergentDefaults = map[string]string{
	"NF_FLOW_API_BASE_URL": "flow-api is the api service on the compose network, not localhost",
}

// readOutsideConfig are the variables the service reads with os.Getenv
// rather than through Config. file is the source that reads it, relative to
// this package.
var readOutsideConfig = []struct {
	name      string
	forwarded bool
	reason    string
	file      string
}{
	{"NF_FLOW_VERSION", true, "version label; unset it falls back to build info", "../lifecycle/lifecycle.go"},
}

// serviceEnvironment returns the service's environment entries, keyed by
// variable name, from the compose file.
func serviceEnvironment(t *testing.T, service string) map[string]string {
	t.Helper()
	f, err := os.Open(composeFile)
	if err != nil {
		t.Fatalf("open %s: %v", composeFile, err)
	}
	defer f.Close()

	header := regexp.MustCompile(`^  ([a-z0-9-]+):$`)
	key := regexp.MustCompile(`^    ([a-z_]+):`)
	entry := regexp.MustCompile(`^      (NF_[A-Z0-9_]+):\s*(.*)$`)
	out := map[string]string{}
	inService, inEnv := false, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := header.FindStringSubmatch(line); m != nil {
			inService, inEnv = m[1] == service, false
			continue
		}
		if !inService {
			continue
		}
		if m := key.FindStringSubmatch(line); m != nil {
			inEnv = m[1] == "environment"
			continue
		}
		if inEnv {
			if m := entry.FindStringSubmatch(line); m != nil {
				out[m[1]] = m[2]
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no %s environment found in %s", service, composeFile)
	}
	return out
}

// configVariables returns every variable Config reads, with its default.
// Tag options (",required", ",notEmpty") are not part of the name.
func configVariables() map[string]string {
	out := map[string]string{}
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			if f.Type.Kind() == reflect.Struct && f.Tag.Get("env") == "" && f.Type.PkgPath() != "time" {
				walk(f.Type)
				continue
			}
			if name, _, _ := strings.Cut(f.Tag.Get("env"), ","); name != "" {
				out[name] = f.Tag.Get("envDefault")
			}
		}
	}
	walk(reflect.TypeOf(Config{}))
	return out
}

// The service environment is the only route from .env to the process, so a
// variable missing from it is ignored however the operator sets it.
func TestComposeProdForwardsEveryConfigVariable(t *testing.T) {
	forwarded := serviceEnvironment(t, composeService)
	read := configVariables()

	for name := range read {
		_, ok := forwarded[name]
		if _, exempt := notForwarded[name]; exempt {
			if ok {
				t.Errorf("%s is listed as not forwarded but compose.prod.yml forwards it", name)
			}
			continue
		}
		if !ok {
			t.Errorf("%s is read by the service but compose.prod.yml does not forward it", name)
		}
	}
	for name := range notForwarded {
		if _, ok := read[name]; !ok {
			t.Errorf("%s is listed as not forwarded but Config does not read it", name)
		}
	}

	outside := map[string]bool{}
	for _, v := range readOutsideConfig {
		outside[v.name] = true
	}
	for name := range forwarded {
		if _, ok := read[name]; ok || outside[name] {
			continue
		}
		t.Errorf("compose.prod.yml forwards %s, which the service does not read", name)
	}
}

// Unset in .env, a forwarded variable has to mean what it means to the
// service when unset, or the stack quietly runs a different default. The
// comparison is by the service-side name; the .env name on the right of a
// reference does not matter.
func TestComposeProdDefaultsMatchConfigDefaults(t *testing.T) {
	ref := regexp.MustCompile(`^\$\{[A-Z0-9_]+:?-([^${}]*)\}$`)
	read := configVariables()
	env := serviceEnvironment(t, composeService)

	for name, reason := range divergentDefaults {
		value, ok := env[name]
		if !ok {
			t.Errorf("%s is listed as divergent (%s) but compose.prod.yml does not set it", name, reason)
			continue
		}
		if got, hasDefault := composeDefault(ref, value); hasDefault && got == read[name] {
			t.Errorf("%s is listed as divergent but compose.prod.yml already matches the config default %q", name, got)
		}
	}
	for name, value := range env {
		want, ok := read[name]
		if !ok {
			continue
		}
		if _, ok := divergentDefaults[name]; ok {
			continue
		}
		got, hasDefault := composeDefault(ref, value)
		if !hasDefault {
			continue
		}
		if got != want {
			t.Errorf("%s defaults to %q in compose.prod.yml but %q in config", name, got, want)
		}
	}
}

// composeDefault returns the value the service sees when .env sets nothing:
// the default of a single ${NAME:-default} reference, or a plain literal.
// Values built from several references or from a required reference have no
// single default and are not comparable.
func composeDefault(ref *regexp.Regexp, value string) (string, bool) {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if m := ref.FindStringSubmatch(value); m != nil {
		return m[1], true
	}
	if strings.Contains(value, "$") || strings.HasPrefix(value, "*") {
		return "", false
	}
	return value, true
}

// Variables read outside Config need the same decision as the ones inside:
// forwarded, or deliberately left out, with the reading source named.
func TestComposeProdOutOfConfigVariables(t *testing.T) {
	forwarded := serviceEnvironment(t, composeService)
	for _, v := range readOutsideConfig {
		src, err := os.ReadFile(v.file)
		if err != nil {
			t.Errorf("%s: %v", v.name, err)
			continue
		}
		if !strings.Contains(string(src), `"`+v.name+`"`) {
			t.Errorf("%s is not read in %s", v.name, v.file)
		}
		if v.reason == "" {
			t.Errorf("%s has no reason", v.name)
		}
		_, ok := forwarded[v.name]
		switch {
		case v.forwarded && !ok:
			t.Errorf("%s is read by the service but compose.prod.yml does not forward it", v.name)
		case !v.forwarded && ok:
			t.Errorf("%s must not be forwarded (%s)", v.name, v.reason)
		}
	}
}
