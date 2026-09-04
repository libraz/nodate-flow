package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
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
// failure) with the provider name, model, the response's token counts, the
// cost in micro-USD, and elapsed, the wall-clock duration of the provider
// call alone. err is nil when the provider call succeeded and carries the
// provider's error otherwise; it is the only reliable success/failure signal,
// since a cost of 0 also means "pricing unknown" for local providers. On a
// failure the token counts are 0, because there is no response to read them
// from. It allows the obs package to record Prometheus metrics without
// creating an import cycle (ai → obs → log → ai).
type InvocationMetricsHook func(provider, model string, inputTokens, outputTokens int, costMicros int64, elapsed time.Duration, err error)

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

// recordMetrics calls the OnInvocation hook if set. err is nil on a
// successful provider call and the provider's error on a failed one;
// elapsed measures the provider call alone.
func (o *Orchestrator) recordMetrics(provider, model string, inputTokens, outputTokens int, costMicros int64, elapsed time.Duration, err error) {
	if o.OnInvocation != nil {
		o.OnInvocation(provider, model, inputTokens, outputTokens, costMicros, elapsed, err)
	}
}

// EventDB is the handle ProposeInboxTriage appends its
// ai.suggestion.proposed rows through. It is a commit boundary rather
// than a bare statement executor because the append wakes subscribers
// that have to be able to read the row.
type EventDB = dbretry.CommitBoundary

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
	CostMicros       int64
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
	ctx = providers.WithWorkspaceID(ctx, workspaceID)

	// Truncate user input to limit prompt-injection surface and prevent
	// prompt-size abuse. See maxDescLen for the rationale.
	req := airequest.New(prov, airequest.Args{
		System: proposeTasksSystem,
		Prompt: sanitizeDesc(signal),
	})
	// time.Since is taken inside each branch rather than bound to a local
	// here: the latency this reports must cover the provider call and
	// nothing else, and the error check has to stay adjacent to the call
	// it checks.
	start := time.Now()
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, 0, 0, 0, time.Since(start), err)
		o.logFailure(ctx, workspaceID, "propose_tasks_from", req, err)
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, resp.InputTokens, resp.OutputTokens, resp.EstimatedCostMicros(), time.Since(start), nil)
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
	ctx = providers.WithWorkspaceID(ctx, workspaceID)
	req := airequest.New(prov, airequest.Args{
		System: proposePrioritySystem,
		Prompt: sanitizeDesc(taskSummary),
	})
	start := time.Now()
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, 0, 0, 0, time.Since(start), err)
		o.logFailure(ctx, workspaceID, "propose_priority", req, err)
		return "", fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, resp.InputTokens, resp.OutputTokens, resp.EstimatedCostMicros(), time.Since(start), nil)
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
//
// vis carries the Layer 4 task-visibility binds for the actor asking
// for the decomposition. The similar tasks are quoted into the prompt
// by title, so the candidate pool is that actor's readable set.
func (o *Orchestrator) ProposeSteps(
	ctx context.Context,
	workspaceID uint32,
	title, description string,
	granularity Granularity,
	existingChildren []ChildTaskSummary,
	embedClient EmbedClient,
	reader SmartCreateReader,
	vis acl.VisibilityArgs,
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
	ctx = providers.WithWorkspaceID(ctx, workspaceID)

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
			queryVec, embedErr := embedClient.EmbedQuery(ctx, workspaceID, taskText)
			if embedErr == nil {
				candidates, listErr := reader.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
					WorkspaceID:   workspaceID,
					Model:         embedClient.Model(),
					TaskID:        0,
					IsElevated:    vis.IsElevated,
					ActorUserID:   vis.ActorUserID,
					ActorUserID_2: vis.ActorUserID,
					ActorUserID_3: vis.ActorUserID,
					Limit:         stepsMaxCandidates,
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
	req := airequest.New(prov, airequest.Args{
		System: proposeStepsSystemPrompt(granularity),
		Prompt: userPrompt,
	})
	start := time.Now()
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, 0, 0, 0, time.Since(start), err)
		o.logFailure(ctx, workspaceID, "propose_steps", req, err)
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, resp.InputTokens, resp.OutputTokens, resp.EstimatedCostMicros(), time.Since(start), nil)
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
	var openCh byte
	var closeCh byte
	for i := 0; i < len(s); i++ {
		if s[i] == '[' || s[i] == '{' {
			start = i
			openCh = s[i]
			if openCh == '[' {
				closeCh = ']'
			} else {
				closeCh = '}'
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
		switch c {
		case openCh:
			depth++
		case closeCh:
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
	model := resp.Model
	if model == "" {
		model = req.Model
	}
	o.LogInvoke(ctx, InvocationRecord{
		WorkspaceID:      workspaceID,
		AgentID:          AgentIDFromContext(ctx),
		Purpose:          purpose,
		Model:            model,
		PromptRedacted:   Redact(strings.TrimSpace(req.System + "\n" + req.Prompt)),
		ResponseRedacted: Redact(resp.Text),
		TokensInput:      resp.InputTokens,
		TokensOutput:     resp.OutputTokens,
		CostMicros:       resp.EstimatedCostMicros(),
		CostCents:        resp.EstimatedCostCents(),
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
		// Provider errors can embed a verbatim upstream response body
		// (e.g. an echoed Authorization header or an API key in a 401
		// detail), so scrub the message before it lands in
		// ai_invocations.error_code — same redactor the prompt/response
		// fields above use, mirroring signaljudge/runner.go.
		ErrorCode: Redact(err.Error()),
	})
}
