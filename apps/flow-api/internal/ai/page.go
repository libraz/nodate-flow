package ai

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// generatePageSystem instructs the LLM to draft the body of a wiki page.
// The prompt asks for plain markdown — no front-matter, no code fences
// wrapping the whole document — so the handler can store the response
// directly without an extraction pass.
const generatePageSystem = `You are a wiki/documentation co-writer for the nodate-flow workspace.
Given a page title and an instruction, write the page body in well-formed Markdown.
- Use a sensible heading hierarchy (start at H2 since the page title is rendered separately).
- Prefer concise sentences and bullet lists over long paragraphs.
- Do NOT prepend YAML/TOML front matter, Markdown frontmatter, or any "---" separator.
- Do NOT wrap the entire response in a fenced code block.
- Do NOT emit prose explaining what you wrote ("Here is the page:" etc.).
- If the instruction is ambiguous, pick the most useful interpretation and proceed.
- Output the Markdown body and nothing else.`

// generatePagePromptMaxLen is the upper bound on the user-supplied
// instruction passed to the LLM. It mirrors the schema validation on
// GeneratePageBody.Prompt (maxLength:10000) but stays internal so the
// orchestrator never panics on an oversized prompt produced by a
// non-HTTP caller (e.g. an MCP tool wrapper).
const generatePagePromptMaxLen = 10000

// pageGenerationMaxTokens caps the LLM completion length for page
// generation. A typical wiki page is well under 4k tokens; larger
// outputs almost always indicate the model is rambling rather than
// writing a useful page.
const pageGenerationMaxTokens = 2048

// GeneratePageBody asks the workspace's default LLM provider to draft a
// page body in Markdown for the given title and free-text instruction.
// The returned string is the trimmed Markdown body suitable for
// persisting directly into pages.body.
//
// Errors:
//   - [ErrNoProvider] when the workspace has no enabled AI provider.
//     Map to AI.PROVIDER.NOT_CONFIGURED at the HTTP boundary.
//   - any other error is wrapped from the provider call (timeout,
//     auth rejection, upstream non-success) and should map to
//     PAGE.GENERATION.UPSTREAM_UNAVAILABLE at the HTTP boundary.
//
// Both the prompt (system + user) and the response are redacted before
// being persisted into ai_invocations.
func (o *Orchestrator) GeneratePageBody(
	ctx context.Context,
	workspaceID uint32,
	title, prompt string,
) (string, error) {
	if o == nil || o.Resolver == nil {
		return "", ErrNoProvider
	}
	if err := o.Guard.Check(ctx, workspaceID); err != nil {
		return "", err
	}
	prov, err := o.Resolver.Default(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if prov == nil {
		return "", ErrNoProvider
	}
	ctx = providers.WithWorkspaceID(ctx, workspaceID)

	userPrompt := buildPagePrompt(sanitizeTitle(title), truncatePrompt(prompt))

	req := providers.Request{
		System:    generatePageSystem,
		Prompt:    userPrompt,
		MaxTokens: pageGenerationMaxTokens,
	}
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, 0)
		o.logFailure(ctx, workspaceID, "generate_page", req, err)
		return "", fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, resp.EstimatedCostMicros())
	o.logSuccess(ctx, workspaceID, "generate_page", req, resp)

	body := strings.TrimSpace(resp.Text)
	if body == "" {
		return "", ErrParse
	}
	return body, nil
}

// buildPagePrompt assembles the user-side prompt for page generation.
func buildPagePrompt(title, prompt string) string {
	var b strings.Builder
	b.WriteString("## Page Title\n")
	b.WriteString(title)
	b.WriteString("\n\n## Instruction\n")
	b.WriteString(prompt)
	return b.String()
}

// truncatePrompt clamps the user instruction to generatePagePromptMaxLen
// runes. The handler already validates length via the Huma schema; this
// is a defence-in-depth backstop for callers that bypass the HTTP layer.
func truncatePrompt(s string) string {
	r := []rune(s)
	if len(r) > generatePagePromptMaxLen {
		return string(r[:generatePagePromptMaxLen])
	}
	return s
}
