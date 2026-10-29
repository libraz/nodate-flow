package helpers

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkdirs(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(root, "sql", r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSchemaLayersIncludeTimeOnlyWhenPresent(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "core/tables", "flow/tables", "flow/constraints")

	if got, want := schemaLayers(root), []string{"core", "flow"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("without sql/time: layers = %v, want %v", got, want)
	}

	mkdirs(t, root, "time/tables", "time/views")

	if got, want := schemaLayers(root), []string{"core", "time", "flow"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("with sql/time: layers = %v, want %v", got, want)
	}

	sql := func(parts ...string) string { return filepath.Join(append([]string{root, "sql"}, parts...)...) }
	if got, want := schemaLayerDirs(root, "tables"), []string{sql("core", "tables"), sql("time", "tables"), sql("flow", "tables")}; !reflect.DeepEqual(got, want) {
		t.Errorf("tables dirs = %v, want %v", got, want)
	}
	// A layer that omits a subdirectory is skipped, not an error.
	if got, want := schemaLayerDirs(root, "constraints"), []string{sql("flow", "constraints")}; !reflect.DeepEqual(got, want) {
		t.Errorf("constraints dirs = %v, want %v", got, want)
	}
	if got, want := schemaLayerDirs(root, "views"), []string{sql("time", "views")}; !reflect.DeepEqual(got, want) {
		t.Errorf("views dirs = %v, want %v", got, want)
	}
}
