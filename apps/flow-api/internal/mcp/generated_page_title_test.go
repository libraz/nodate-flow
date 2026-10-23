package mcp_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// cannedProvider answers every completion with a fixed body. It stands in
// for the workspace's LLM so a test can decide exactly what the model
// returns — which is the only way to drive a response no real prompt can
// be relied on to produce.
type cannedProvider struct {
	text string
}

func (p *cannedProvider) Name() string         { return "canned" }
func (p *cannedProvider) Kind() providers.Kind { return providers.Kind("openai_compat") }
func (p *cannedProvider) Model() string        { return "canned-model" }
func (p *cannedProvider) Complete(_ context.Context, _ providers.Request) (*providers.Response, error) {
	return &providers.Response{Model: "canned-model", Text: p.text}, nil
}

// TestGeneratePageClipsAnOverlongBrief proves generate_page stores a page
// when the brief it builds the title from is far longer than the title
// bound, and that what it stores is intact UTF-8.
//
// A byte-indexed clip would cut through the middle of a character, which
// the utf8mb4 column rejects under STRICT_TRANS_TABLES, losing the body the
// call had already been billed for. The assertions cover both the length
// and the rune boundary.
func TestGeneratePageClipsAnOverlongBrief(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPVisibilityFixture(t, db)
	ctx := context.Background()

	// A brief built from multi-byte runes: 400 characters, 1200 bytes, far
	// past the title bound, so a cut on a raw byte index would land inside
	// a character.
	brief := strings.Repeat("設計方針の要約", 100)
	require.Greater(t, len(brief), 500, "fixture brief must exceed pages.title")

	orch := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return &cannedProvider{text: "Body written by the model."}, nil
		}),
	}
	deps := mcp.Deps{DB: db, Queries: generated.New(db), AI: orch}
	caller := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

	out, err := mcp.RunGeneratePage(ctx, deps, caller, mcpVisJSON(t, map[string]any{
		"contextDescription": brief,
	}))
	require.NoError(t, err, "an overlong brief must not cost the caller the page")

	res, ok := out.(map[string]any)
	require.True(t, ok)
	pageID, ok := res["id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, pageID)

	var storedTitle, storedBody string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT title, body FROM pages WHERE public_id = UUID_TO_BIN(?, 0) AND workspace_id = ?`,
		pageID, fx.wsID).Scan(&storedTitle, &storedBody))

	// Valid UTF-8 plus "is a prefix of the full title" is what pins the cut
	// to a rune boundary: a byte-indexed cut through a character leaves a
	// fragment, and a fragment is neither.
	require.True(t, utf8.ValidString(storedTitle),
		"stored title must be valid UTF-8, got %q", storedTitle)
	require.LessOrEqual(t, len(storedTitle), 500,
		"stored title must fit pages.title, got %d bytes", len(storedTitle))
	require.True(t, strings.HasPrefix("Generated: "+brief, storedTitle),
		"stored title must be a prefix of the one built from the brief, got %q", storedTitle)
	require.Equal(t, "Body written by the model.", storedBody,
		"the body the call paid for must survive")
}
