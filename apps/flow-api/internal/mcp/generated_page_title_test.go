package mcp_test

import (
	"context"
	"encoding/json"
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

// TestGeneratePageStoresAnOverlongModelTitle proves the generate_page tool
// stores a page even when the model answers with a title far longer than
// pages.title, and that what it stores is intact UTF-8.
//
// Nothing constrains a model's output. The tool used the proposed title
// verbatim, so a long answer failed the insert under STRICT_TRANS_TABLES
// and the caller lost the whole generated page — including the body the
// call had already been billed for. A byte-indexed clip would trade that
// for a title cut through the middle of a character, which the utf8mb4
// column rejects in the same way, so the assertions below cover both the
// length and the rune boundary.
func TestGeneratePageStoresAnOverlongModelTitle(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPVisibilityFixture(t, db)
	ctx := context.Background()

	// A title built from multi-byte runes: 400 characters, 1200 bytes,
	// well past pages.title and past any byte-denominated clip, so a cut
	// on a raw byte index would land inside a character.
	modelTitle := strings.Repeat("設計方針の要約", 100)
	require.Greater(t, len(modelTitle), 500, "fixture title must exceed pages.title")
	proposal, err := json.Marshal([]map[string]string{{
		"title":       modelTitle,
		"description": "Body written by the model.",
		"priority":    "medium",
	}})
	require.NoError(t, err)

	orch := &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return &cannedProvider{text: string(proposal)}, nil
		}),
	}
	deps := mcp.Deps{DB: db, Queries: generated.New(db), AI: orch}
	caller := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

	out, err := mcp.RunGeneratePage(ctx, deps, caller, mcpVisJSON(t, map[string]any{
		"contextDescription": "Summarise the design decisions.",
	}))
	require.NoError(t, err, "an overlong model title must not cost the caller the page")

	res, ok := out.(map[string]any)
	require.True(t, ok)
	// Without this the test would still pass on the fallback path, which
	// never sees the model's title at all.
	require.Equal(t, true, res["isAiGenerated"], "the model branch must be the one under test")
	pageID, ok := res["id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, pageID)

	var storedTitle, storedBody string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT title, body FROM pages WHERE public_id = UUID_TO_BIN(?, 0) AND workspace_id = ?`,
		pageID, fx.wsID).Scan(&storedTitle, &storedBody))

	// Valid UTF-8 plus "is a prefix of what the model said" is what pins
	// the cut to a rune boundary: a byte-indexed cut through a character
	// leaves a fragment, and a fragment is neither.
	require.True(t, utf8.ValidString(storedTitle),
		"stored title must be valid UTF-8, got %q", storedTitle)
	require.LessOrEqual(t, len(storedTitle), 500,
		"stored title must fit pages.title, got %d bytes", len(storedTitle))
	require.NotEmpty(t, storedTitle)
	require.True(t, strings.HasPrefix(modelTitle, storedTitle),
		"stored title must be a prefix of the model's, got %q", storedTitle)
	require.Equal(t, "Body written by the model.", storedBody,
		"the body the call paid for must survive")
}
