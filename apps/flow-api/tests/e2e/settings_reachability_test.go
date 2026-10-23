package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// settingConsumer names the file that reads a stored value and a symbol
// that has to appear in it.
type settingConsumer struct {
	File   string `json:"file"`
	Symbol string `json:"symbol"`
}

// settingInventory mirrors scripts/setting-consumers.json.
type settingInventory struct {
	Tables map[string]struct {
		Consumers   map[string]settingConsumer `json:"consumers"`
		NotSettings map[string]string          `json:"notSettings"`
	} `json:"tables"`
	ConstraintOps    map[string]settingConsumer `json:"constraintOps"`
	InstanceSettings map[string]settingConsumer `json:"instanceSettings"`
}

var (
	// sqlColumn matches a column definition line inside CREATE TABLE.
	sqlColumn = regexp.MustCompile(`(?m)^\s+([a-z_][a-z0-9_]*)\s+[A-Z]`)
	// constraintOpDecl matches one `OpX Op = "name"` declaration.
	constraintOpDecl = regexp.MustCompile(`\bOp[A-Za-z]+\s+Op\s*=\s*"([^"]+)"`)
	// quotedKey matches a quoted map key.
	quotedKey = regexp.MustCompile(`"([^"]+)"\s*:`)
)

// TestEveryStoredSettingHasAConsumer holds each value a user can store
// that claims to change behaviour to the code that reads it. The columns
// of every inventoried table, the constraint DSL ops and the instance
// setting keys the admin API accepts are read from the sources; each has
// to be mapped in scripts/setting-consumers.json to a file that still
// contains its consumer symbol, or be declared as changing no behaviour.
// A value added without a reader, or a reader removed, fails here.
func TestEveryStoredSettingHasAConsumer(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..", "..")
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	var inv settingInventory
	if err := json.Unmarshal([]byte(read("scripts/setting-consumers.json")), &inv); err != nil {
		t.Fatalf("parse scripts/setting-consumers.json: %v", err)
	}
	checkConsumer := func(what string, c settingConsumer) {
		t.Helper()
		if !strings.Contains(read(c.File), c.Symbol) {
			t.Errorf("%s: consumer %q no longer appears in %s; wire the value again or remove it from the API", what, c.Symbol, c.File)
		}
	}

	if len(inv.Tables) == 0 {
		t.Fatal("the inventory lists no table, so nothing was checked")
	}
	for table, entry := range inv.Tables {
		src := read(filepath.Join("sql", "flow", "tables", table+".sql"))
		body := src[strings.Index(src, "CREATE TABLE"):]
		cols := map[string]bool{}
		for _, m := range sqlColumn.FindAllStringSubmatch(body, -1) {
			switch m[1] {
			case "KEY", "UNIQUE", "CONSTRAINT", "PRIMARY", "FULLTEXT":
				continue
			}
			cols[m[1]] = true
		}
		if len(cols) == 0 {
			t.Fatalf("%s: no column parsed from the schema", table)
		}
		for col := range cols {
			c, mapped := entry.Consumers[col]
			_, declared := entry.NotSettings[col]
			switch {
			case mapped:
				checkConsumer(table+"."+col, c)
			case !declared:
				t.Errorf("%s.%s has no consumer in scripts/setting-consumers.json; map it to the code that reads it, or list it under notSettings with why it changes no behaviour", table, col)
			}
		}
		for col := range entry.Consumers {
			if !cols[col] {
				t.Errorf("%s.%s is inventoried but no longer in the schema", table, col)
			}
		}
	}

	ops := constraintOpDecl.FindAllStringSubmatch(read("apps/flow-api/internal/constraint/dsl.go"), -1)
	if len(ops) == 0 {
		t.Fatal("no constraint op parsed from dsl.go")
	}
	for _, m := range ops {
		c, ok := inv.ConstraintOps[m[1]]
		if !ok {
			t.Errorf("constraint op %q has no fact source in scripts/setting-consumers.json", m[1])
			continue
		}
		checkConsumer("constraint op "+m[1], c)
	}

	settings := read("apps/auth-api/internal/http/handlers/admin/settings.go")
	start := strings.Index(settings, "var settingValidators")
	if start < 0 {
		t.Fatal("settingValidators not found in the admin settings handler")
	}
	block := settings[start:]
	open := strings.Index(block, "bool{")
	if open < 0 {
		t.Fatal("settingValidators literal not found in the admin settings handler")
	}
	block = block[open+len("bool"):]
	depth, end := 0, 0
	for i, r := range block {
		if r == '{' {
			depth++
		} else if r == '}' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}
	block = block[:end]
	for _, m := range quotedKey.FindAllStringSubmatch(block, -1) {
		c, ok := inv.InstanceSettings[m[1]]
		if !ok {
			t.Errorf("instance setting %q is accepted by PATCH /admin/settings but has no consumer in scripts/setting-consumers.json", m[1])
			continue
		}
		checkConsumer("instance setting "+m[1], c)
	}
}
