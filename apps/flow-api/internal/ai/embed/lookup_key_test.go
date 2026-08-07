package embed

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// keyRecordingStore records the model key of every read and write so a
// test can compare the two without knowing what either should be.
type keyRecordingStore struct {
	reads  []string
	writes []string
}

func (s *keyRecordingStore) GetTaskEmbedding(_ context.Context, arg generated.GetTaskEmbeddingParams) (generated.GetTaskEmbeddingRow, error) {
	s.reads = append(s.reads, arg.Model)
	return generated.GetTaskEmbeddingRow{}, sql.ErrNoRows
}

func (s *keyRecordingStore) UpsertTaskEmbedding(_ context.Context, arg generated.UpsertTaskEmbeddingParams) error {
	s.writes = append(s.writes, arg.Model)
	return nil
}

// TestEmbeddingsAreStoredUnderTheClientsModelKey pins that the key rows
// are written under is the same key [Client.Model] reports, whatever the
// provider happens to be called.
//
// The provider name here is deliberately arbitrary rather than either
// real model name. Duplicate detection was broken in production and
// green in CI for exactly this reason: the tests asserted the mock's
// literal model key, so nothing noticed that readers resolved their key
// from a different source than writers used. A test that names a model
// cannot see that class of defect; one that compares the two sides can.
func TestEmbeddingsAreStoredUnderTheClientsModelKey(t *testing.T) {
	t.Parallel()

	store := &keyRecordingStore{}
	client := New(&fakeProvider{model: "some-arbitrary-embedding-model"}, store)

	if err := client.EmbedTask(context.Background(), 7, 42, "Title", "Description"); err != nil {
		t.Fatalf("EmbedTask: %v", err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("expected one write, got %d", len(store.writes))
	}
	if store.writes[0] != client.Model() {
		t.Errorf("rows are written under %q but Model() reports %q; every reader keys off Model(), "+
			"so any divergence makes duplicate detection return nothing at all",
			store.writes[0], client.Model())
	}
	for i, read := range store.reads {
		if read != client.Model() {
			t.Errorf("read %d used key %q, want %q", i, read, client.Model())
		}
	}
}

// TestModelIsNilSafe covers the deployment with no embedder wired: a
// workspace with no embeddings must yield an empty key that matches
// nothing, not a panic on the duplicates endpoint.
func TestModelIsNilSafe(t *testing.T) {
	t.Parallel()

	var c *Client
	if got := c.Model(); got != "" {
		t.Fatalf("Model() on a nil client = %q, want empty", got)
	}
	if got := (&Client{}).Model(); got != "" {
		t.Fatalf("Model() with no provider = %q, want empty", got)
	}
}

// TestEmbeddingLookupsKeyOffTheEmbedder walks the module and requires
// that every file addressing task_embeddings by model asks an embedder
// what that model is — and that none of them takes it from
// ai_settings.embed_model instead.
//
// That column is not the embedding model. Nothing in the product writes
// it: no API accepts it, and the embedder is chosen by deployment
// configuration, so it sits at its DDL default of "mock-768" forever.
// Using it as the lookup key meant asking for rows filed under
// "mock-768" on a deployment whose embedder had filed every row under
// its real model name — so duplicate detection, the relation-suggestion
// pipeline, and the propose_duplicates MCP tool all returned nothing at
// all, while returning everything under the mock the tests ran against.
//
// The guard is source-level because these read paths take a concrete
// *generated.Queries and cannot be driven without a database, which is
// precisely how the divergence survived: they were only ever exercised
// end to end against the mock, where the two keys happened to agree.
func TestEmbeddingLookupsKeyOffTheEmbedder(t *testing.T) {
	t.Parallel()

	keyedParams := []string{"GetTaskEmbeddingParams{", "ListCandidateTaskEmbeddingsParams{"}
	var noEmbedder, fromSettings, inspected []string

	forEachSourceFile(t, func(rel string, src string) {
		if strings.HasPrefix(rel, "internal/db/generated/") {
			return
		}
		addressesEmbeddings := false
		for _, p := range keyedParams {
			if strings.Contains(src, p) {
				addressesEmbeddings = true
			}
		}
		if !addressesEmbeddings {
			return
		}
		inspected = append(inspected, rel)
		if !strings.Contains(src, ".Model()") {
			noEmbedder = append(noEmbedder, rel)
		}
		if strings.Contains(src, "EmbedModel") {
			fromSettings = append(fromSettings, rel)
		}
	})

	// This is a "no file does X" assertion, which a matcher that stopped
	// matching anything satisfies just as well as a clean tree. The read
	// paths are known and finite, so requiring that they were actually
	// found turns a renamed sqlc params type from a quiet pass into a
	// loud failure.
	for _, want := range []string{
		"internal/ai/embed/embed.go",
		"internal/http/handlers/tasks/duplicates.go",
		"internal/mcp/tools.go",
		"internal/ai/relations/pipeline.go",
	} {
		if !slices.Contains(inspected, want) {
			t.Errorf("%s addresses task_embeddings but this guard did not inspect it; "+
				"the sqlc params type was probably renamed and the match list needs updating "+
				"(inspected: %v)", want, inspected)
		}
	}

	if len(noEmbedder) > 0 {
		t.Errorf("task_embeddings must be addressed by the embedder's own model key (embed.Client.Model); "+
			"these files pick a model from somewhere else:\n  %s", strings.Join(noEmbedder, "\n  "))
	}
	if len(fromSettings) > 0 {
		t.Errorf("ai_settings.embed_model has no write path and never leaves its column default, "+
			"so it cannot address embedding rows; these files reach for it:\n  %s",
			strings.Join(fromSettings, "\n  "))
	}
}

// forEachSourceFile calls fn for every non-generated, non-test Go file
// in the flow-api module, with a slash-separated path relative to the
// module root.
func forEachSourceFile(t *testing.T, fn func(rel, src string)) {
	t.Helper()
	root := moduleRoot(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		// The walk root is this repository's own source tree, supplied by
		// the test, not by anything a caller controls.
		b, readErr := os.ReadFile(path) //#nosec G122 -- walk root is the repo source tree, fixed by the test
		if readErr != nil {
			return readErr
		}
		fn(filepath.ToSlash(rel), string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
}

// moduleRoot returns the apps/flow-api directory. Tests run in the
// package directory, so the module root is three levels up from
// internal/ai/embed.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the flow-api module root at %s: %v", root, err)
	}
	return root
}
