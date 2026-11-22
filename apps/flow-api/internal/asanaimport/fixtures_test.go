package asanaimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// repoRoot is the repository root; tests run in apps/flow-api/internal/asanaimport.
const repoRoot = "../../../.."

// readCoreFixture reads a shared importer fixture from sql/core/importers.
func readCoreFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "sql", "core", "importers", name))
	require.NoError(t, err)
	return b
}

type expectedIssue struct {
	Path string `yaml:"path"`
	Kind string `yaml:"kind"`
}

type mappingFixtureCase struct {
	Name      string          `yaml:"name"`
	DryRun    bool            `yaml:"dry_run"`
	Raw       *string         `yaml:"raw"`
	Mapping   *Mapping        `yaml:"mapping"`
	Container *MappingProject `yaml:"container"`
	Expect    []expectedIssue `yaml:"expect"`
}

type mappingFixture struct {
	Version int                  `yaml:"version"`
	Cases   []mappingFixtureCase `yaml:"cases"`
}

func loadMappingFixture(t *testing.T) mappingFixture {
	t.Helper()
	var f mappingFixture
	require.NoError(t, yaml.Unmarshal(readCoreFixture(t, "mapping-common.yaml"), &f))
	require.Equal(t, 1, f.Version)
	require.NotEmpty(t, f.Cases)
	return f
}
