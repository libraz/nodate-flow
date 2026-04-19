package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// ErrNoProvider is returned by ProposeTasksFrom / ProposePriority when the
// workspace has no enabled AI provider configured. Map to
// AI.PROVIDER.NOT_CONFIGURED at the HTTP boundary.
var ErrNoProvider = errors.New("ai: no provider configured for workspace")

// ErrParse is returned when the LLM response could not be parsed into the
// expected JSON shape.
var ErrParse = errors.New("ai: failed to parse provider response")

// ProposedTask is the LLM-generated task suggestion.
type ProposedTask struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

// ProviderResolver is the narrow contract for picking the workspace's
// default Provider. Production code wires this to a function that calls
// ListProvidersForWorkspace + FindProviderForDecrypt + providers.New, all
// inside the providers/ package family. Tests inject a fake.
type ProviderResolver interface {
	Default(ctx context.Context, workspaceID uint32) (providers.Provider, error)
}

// ProviderResolverFunc adapts a plain function to ProviderResolver.
type ProviderResolverFunc func(ctx context.Context, workspaceID uint32) (providers.Provider, error)

// Default implements ProviderResolver.
func (f ProviderResolverFunc) Default(ctx context.Context, workspaceID uint32) (providers.Provider, error) {
	return f(ctx, workspaceID)
}

// InvocationMetricsHook is called after every LLM provider call (success or
// failure) with the provider name, model, workspace ID, and cost in cents.
// It allows the obs package to record Prometheus metrics without creating
// an import cycle (ai → obs → log → ai).
type InvocationMetricsHook func(provider, model, workspaceID string, costCents int64)

// Orchestrator wires a Provider source, the cost guard, and an invocation
// logger. The HTTP and MCP layers depend on this struct rather than calling
// providers.New directly so that depguard can keep crypto access fenced
// inside internal/ai/providers.
type Orchestrator struct {
	Resolver  ProviderResolver
	Guard     *CostGuard
	LogInvoke InvocationLogger
	// OnInvocation is an optional hook called after each LLM call for
	// metrics recording. Wired to obs.RecordAIInvocation at startup.
	OnInvocation InvocationMetricsHook

	// DB and Queries are optional dependencies used by
	// orchestration methods that need to read project state (for
	// example ProposeInboxTriage reads the workspace inbox) and append
	// ai.suggestion.* events. They may be left nil for code paths
	// that only call ProposeTasksFrom / ProposePriority.
	DB      EventDB
	Queries InboxReader

	// ProposalCache is an optional short-lived cache for LLM proposal
	// results. When set, identical requests (smart create, inbox
	// triage) within the TTL window return the cached result instead
	// of making a redundant LLM call. Nil disables caching.
	ProposalCache *ProposalCache
}

// recordMetrics calls the OnInvocation hook if set.
func (o *Orchestrator) recordMetrics(provider, model, wsID string, costCents int64) {
	if o.OnInvocation != nil {
		o.OnInvocation(provider, model, wsID, costCents)
	}
}

// EventDB is the narrow surface ProposeInboxTriage needs to append a
// row to the events table. It is satisfied by *sql.DB and *sql.Tx.
type EventDB interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// InboxReader is the narrow contract ProposeInboxTriage uses to fetch
// the top-N inbox items for a workspace. The production wiring passes
// the sqlc Queries handle.
type InboxReader interface {
	ListInbox(ctx context.Context, arg generated.ListInboxParams) ([]generated.ListInboxRow, error)
}

// InvocationLogger persists a redacted record of the LLM call. The
// orchestrator already runs Redact on prompt and response before calling
// this hook.
type InvocationLogger func(ctx context.Context, rec InvocationRecord)

// InvocationRecord is the redacted ai_invocations payload. ai_invocations
// rows are append-only; status is one of "ok", "error".
type InvocationRecord struct {
	WorkspaceID      uint32
	AgentID          uint32 // non-zero when the call was made on behalf of an AI agent
	Purpose          string
	Model            string
	PromptRedacted   string
	ResponseRedacted string
	TokensInput      int
	TokensOutput     int
	CostCents        int64
	Status           string
	ErrorCode        string
}

// maxTitleLen is the maximum number of characters accepted for a
// user-supplied task title before it is truncated. This limits the
// surface area for prompt injection and prevents excessively large
// prompts from being sent to the LLM provider.
const maxTitleLen = 500

// maxDescLen is the maximum number of characters accepted for a
// user-supplied task description before it is truncated.
const maxDescLen = 5000

// sanitizeTitle truncates a user-supplied title to maxTitleLen runes.
// SECURITY: User-supplied content is included verbatim in LLM prompts.
// While full prompt-injection mitigation requires output validation
// (not input sanitization alone), truncating input reduces the attack
// surface and prevents prompt-size abuse.
func sanitizeTitle(s string) string {
	r := []rune(s)
	if len(r) > maxTitleLen {
		return string(r[:maxTitleLen])
	}
	return s
}

// sanitizeDesc truncates a user-supplied description to maxDescLen
// runes. See sanitizeTitle for the security rationale.
func sanitizeDesc(s string) string {
	r := []rune(s)
	if len(r) > maxDescLen {
		return string(r[:maxDescLen])
	}
	return s
}

const proposeTasksSystem = `You are a task-planning assistant for the nodate-flow workspace. ` +
	`Reply ONLY with a JSON array of objects with keys "title", "description", "priority". ` +
	`priority is one of "low", "medium", "high".`

const proposePrioritySystem = `You are a task-priority assistant. ` +
	`Reply ONLY with a JSON object {"priority": "low" | "medium" | "high", "reason": "..."}.`

// ProposeTasksFrom asks the workspace's default LLM provider to convert a
// free-text signal into a list of proposed tasks. Both the prompt and the
// response are redacted before logging.
func (o *Orchestrator) ProposeTasksFrom(ctx context.Context, workspaceID uint32, signal string) ([]ProposedTask, error) {
	if o == nil || o.Resolver == nil {
		return nil, ErrNoProvider
	}
	if err := o.Guard.Check(ctx, workspaceID); err != nil {
		return nil, err
	}
	prov, err := o.Resolver.Default(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if prov == nil {
		return nil, ErrNoProvider
	}

	// Truncate user input to limit prompt-injection surface and prevent
	// prompt-size abuse. See maxDescLen for the rationale.
	req := providers.Request{
		System: proposeTasksSystem,
		Prompt: sanitizeDesc(signal),
	}
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, 0)
		o.logFailure(ctx, workspaceID, "propose_tasks_from", req, err)
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, resp.CostCents)
	o.logSuccess(ctx, workspaceID, "propose_tasks_from", req, resp)

	tasks, parseErr := parseProposedTasks(resp.Text)
	if parseErr != nil {
		return nil, parseErr
	}
	return tasks, nil
}

// ProposePriority asks the LLM to suggest a priority for a task given a
// short description.
func (o *Orchestrator) ProposePriority(ctx context.Context, workspaceID uint32, taskSummary string) (string, error) {
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
	req := providers.Request{
		System: proposePrioritySystem,
		Prompt: sanitizeDesc(taskSummary),
	}
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, 0)
		o.logFailure(ctx, workspaceID, "propose_priority", req, err)
		return "", fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, resp.CostCents)
	o.logSuccess(ctx, workspaceID, "propose_priority", req, resp)

	var parsed struct {
		Priority string `json:"priority"`
	}
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); err != nil {
		return "", fmt.Errorf("%w: %v", ErrParse, err)
	}
	return parsed.Priority, nil
}

// --------------------------------------------------------------------
// ProposeSteps — context-aware step decomposition
// --------------------------------------------------------------------

// Granularity controls how many steps the LLM proposes.
type Granularity string

const (
	// GranularityCoarse requests 3-5 high-level steps.
	GranularityCoarse Granularity = "coarse"
	// GranularityStandard requests 5-8 steps (default).
	GranularityStandard Granularity = "standard"
	// GranularityFine requests 8-15 detailed steps.
	GranularityFine Granularity = "fine"
)

// ChildTaskSummary is a lightweight representation of an existing child
// task, passed to ProposeSteps so the LLM avoids duplicating them.
type ChildTaskSummary struct {
	Title string
	State string
}

// granularityRange returns the (min, max) step counts for a Granularity.
func granularityRange(g Granularity) (int, int) {
	switch g {
	case GranularityCoarse:
		return 3, 5
	case GranularityFine:
		return 8, 15
	default:
		return 5, 8
	}
}

// proposeStepsSystemPrompt builds a granularity-aware system prompt.
func proposeStepsSystemPrompt(g Granularity) string {
	nMin, nMax := granularityRange(g)
	return fmt.Sprintf(
		"You are a task-planning assistant.\n"+
			"Break the given task into %d-%d concrete execution steps.\n"+
			"- Do NOT duplicate any items listed under \"Existing Subtasks\".\n"+
			"- Learn from decomposition patterns in \"Similar Past Tasks\" if provided.\n"+
			"- Reply ONLY with a JSON array of objects with keys \"title\", \"description\", \"priority\".\n"+
			"- priority is one of \"low\", \"medium\", \"high\".\n"+
			"- Return ONLY valid JSON, no prose, no markdown fences.",
		nMin, nMax,
	)
}

const (
	stepsMaxCandidates = 200
	stepsTopN          = 10
)

// ProposeSteps asks the workspace's default LLM provider to decompose a
// task into subtasks, with awareness of existing child tasks (to avoid
// duplicates) and similar past tasks (via embedding similarity). The
// embedClient and reader may be nil; in that case, similar-task context
// is omitted and only the task text and existing children are used.
func (o *Orchestrator) ProposeSteps(
	ctx context.Context,
	workspaceID uint32,
	title, description string,
	granularity Granularity,
	existingChildren []ChildTaskSummary,
	embedClient EmbedClient,
	reader SmartCreateReader,
) ([]ProposedTask, error) {
	// ---- guard ----
	if o == nil || o.Resolver == nil {
		return nil, ErrNoProvider
	}
	if err := o.Guard.Check(ctx, workspaceID); err != nil {
		return nil, err
	}
	prov, err := o.Resolver.Default(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if prov == nil {
		return nil, ErrNoProvider
	}

	// ---- cache check ----
	cacheKey := ProposalCacheKey(workspaceID, "propose_steps", title, description, string(granularity))
	if cached, ok := o.ProposalCache.Get(cacheKey); ok {
		if tasks, _ := cached.([]ProposedTask); tasks != nil {
			return tasks, nil
		}
	}

	// ---- find similar past tasks (optional) ----
	var ranked []scoredTask
	if embedClient != nil && reader != nil {
		taskText := composeText(title, description)
		if taskText != "" {
			queryVec, embedErr := embedClient.Embed(ctx, taskText)
			if embedErr == nil {
				embed.Normalize(queryVec)
				candidates, listErr := reader.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
					WorkspaceID: workspaceID,
					Model:       embedClient.Model(),
					TaskID:      0,
					Limit:       stepsMaxCandidates,
				})
				if listErr == nil {
					ranked = make([]scoredTask, 0, len(candidates))
					for _, c := range candidates {
						vec, derr := embed.Decode(toBytes(c.Vector))
						if derr != nil || len(vec) != len(queryVec) {
							continue
						}
						sim := embed.Cosine(queryVec, vec)
						ranked = append(ranked, scoredTask{id: c.ID, title: c.Title, score: sim})
					}
					sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
					if len(ranked) > stepsTopN {
						ranked = ranked[:stepsTopN]
					}
				}
			}
		}
	}

	// ---- build user prompt ----
	// Truncate user input before building the LLM prompt.
	userPrompt := buildStepsPrompt(sanitizeTitle(title), sanitizeDesc(description), existingChildren, ranked)

	// ---- call LLM ----
	req := providers.Request{
		System: proposeStepsSystemPrompt(granularity),
		Prompt: userPrompt,
	}
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, 0)
		o.logFailure(ctx, workspaceID, "propose_steps", req, err)
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, resp.CostCents)
	o.logSuccess(ctx, workspaceID, "propose_steps", req, resp)

	// ---- parse response ----
	tasks, parseErr := parseProposedTasks(resp.Text)
	if parseErr != nil {
		return nil, parseErr
	}
	o.ProposalCache.Put(cacheKey, tasks)
	return tasks, nil
}

// buildStepsPrompt assembles the user prompt for step decomposition,
// including the task to decompose, existing children, and similar tasks.
func buildStepsPrompt(
	title, description string,
	children []ChildTaskSummary,
	similar []scoredTask,
) string {
	var b strings.Builder

	b.WriteString("## Task to Decompose\n")
	b.WriteString("Title: ")
	b.WriteString(title)
	b.WriteByte('\n')
	if description != "" {
		b.WriteString("Description:\n")
		b.WriteString(description)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	b.WriteString("## Existing Subtasks\n")
	if len(children) == 0 {
		b.WriteString("None\n")
	} else {
		for _, c := range children {
			fmt.Fprintf(&b, "- \"%s\" [%s]\n", c.Title, c.State)
		}
	}
	b.WriteByte('\n')

	if len(similar) > 0 {
		b.WriteString("## Similar Past Tasks\n")
		for _, s := range similar {
			fmt.Fprintf(&b, "- \"%s\" (similarity: %.2f)\n", s.title, s.score)
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// parseProposedTasks tolerates the model wrapping the JSON array in prose
// or fenced code blocks. extractJSON pulls the first balanced "[ ... ]"
// substring before json.Unmarshal runs.
func parseProposedTasks(s string) ([]ProposedTask, error) {
	payload := extractJSON(s)
	if payload == "" {
		return nil, ErrParse
	}
	var tasks []ProposedTask
	if err := json.Unmarshal([]byte(payload), &tasks); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	return tasks, nil
}

// extractJSON returns the first JSON object or array literal it finds in s.
// Hand-written, no regex. Returns "" when nothing balanced is found.
func extractJSON(s string) string {
	start := -1
	var open byte
	var close byte
	for i := 0; i < len(s); i++ {
		if s[i] == '[' || s[i] == '{' {
			start = i
			open = s[i]
			if open == '[' {
				close = ']'
			} else {
				close = '}'
			}
			break
		}
	}
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == open {
			depth++
		} else if c == close {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func (o *Orchestrator) logSuccess(ctx context.Context, workspaceID uint32, purpose string, req providers.Request, resp *providers.Response) {
	if o.LogInvoke == nil {
		return
	}
	o.LogInvoke(ctx, InvocationRecord{
		WorkspaceID:      workspaceID,
		AgentID:          AgentIDFromContext(ctx),
		Purpose:          purpose,
		Model:            req.Model,
		PromptRedacted:   Redact(strings.TrimSpace(req.System + "\n" + req.Prompt)),
		ResponseRedacted: Redact(resp.Text),
		TokensInput:      resp.InputTokens,
		TokensOutput:     resp.OutputTokens,
		CostCents:        resp.CostCents,
		Status:           "ok",
	})
}

func (o *Orchestrator) logFailure(ctx context.Context, workspaceID uint32, purpose string, req providers.Request, err error) {
	if o.LogInvoke == nil {
		return
	}
	o.LogInvoke(ctx, InvocationRecord{
		WorkspaceID:    workspaceID,
		AgentID:        AgentIDFromContext(ctx),
		Purpose:        purpose,
		Model:          req.Model,
		PromptRedacted: Redact(strings.TrimSpace(req.System + "\n" + req.Prompt)),
		Status:         "error",
		ErrorCode:      err.Error(),
	})
}
