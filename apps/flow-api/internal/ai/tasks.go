package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/suggestionprivacy"
)

// ErrNoProvider is returned by ProposeTasksFrom / ProposePriority when the
// workspace has no enabled AI provider configured. Map to
// AI.PROVIDER.NOT_CONFIGURED at the HTTP boundary.
var ErrNoProvider = errors.New("ai: no provider configured for workspace")

// ErrParse is returned when the LLM response could not be parsed into the
// expected JSON shape.
var ErrParse = errors.New("ai: failed to parse provider response")

// ErrProposalInputsChanged is returned when a proposal's quoted task inputs,
// or the caller's access to them, changed around the provider call. Map to
// AI.PROPOSAL.INPUTS_CHANGED at the HTTP boundary.
var ErrProposalInputsChanged = errors.New("ai: proposal inputs changed")

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

// InvocationMetricsHook is called after each validated provider attempt with
// the provider name, model, the decoded response's token counts and cost, and
// elapsed, the wall-clock duration of the provider call alone. err describes
// the final attempt outcome, including incomplete or rejected decoded output.
// A decoded rejection still carries its actual usage. It allows the obs
// package to record Prometheus metrics without creating an import cycle
// (ai → obs → log → ai).
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
	ProposalCache     *ProposalCache
	SuggestionPrivacy *suggestionprivacy.Store
}

// recordMetrics calls the OnInvocation hook if set. err is nil on a
// successful validated attempt and describes the final attempt outcome
// otherwise; elapsed measures the provider call alone.
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
// rows are append-only; status is one of "ok", "error", or "blocked".
type InvocationRecord struct {
	WorkspaceID      uint32
	AgentID          uint32 // non-zero when the call was made on behalf of an AI agent
	TaskID           uint32 // non-zero when the prompt quotes one task's content
	Audience         InvocationAudience
	InputResources   []InvocationResource
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
	if _, err := CheckBudget(ctx, o.Guard, workspaceID); err != nil {
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
		Output: tasksFromOutput,
	})
	var tasks []ProposedTask
	_, err = o.CompleteAndRecord(ctx, workspaceID, "propose_tasks_from", prov, req, func(resp *providers.Response) error {
		payload, err := outputPayload(resp, tasksFromOutput)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &tasks); err != nil {
			return fmt.Errorf("%w: task response could not be decoded", providers.ErrResponseSchemaMismatch)
		}
		return validateProposedTaskFields(tasks)
	})
	if err != nil {
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	return tasks, nil
}

// ProposePriority asks the LLM to suggest a priority for a task given a
// short description.
func (o *Orchestrator) ProposePriority(ctx context.Context, workspaceID uint32, taskSummary string) (string, error) {
	if o == nil || o.Resolver == nil {
		return "", ErrNoProvider
	}
	if _, err := CheckBudget(ctx, o.Guard, workspaceID); err != nil {
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
		Output: priorityOutput,
	})
	var parsed struct {
		Priority string `json:"priority"`
	}
	_, err = o.CompleteAndRecord(ctx, workspaceID, "propose_priority", prov, req, func(resp *providers.Response) error {
		payload, err := outputPayload(resp, priorityOutput)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &parsed); err != nil {
			return fmt.Errorf("%w: priority response could not be decoded", providers.ErrResponseSchemaMismatch)
		}
		if !validPriorityLabel(parsed.Priority) {
			return fmt.Errorf("%w: priority is outside the supported vocabulary", providers.ErrResponseSchemaMismatch)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("ai: provider call failed: %w", err)
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
	PublicID string
	Title    string
	State    string
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
	if _, err := CheckBudget(ctx, o.Guard, workspaceID); err != nil {
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
	proposalInputsReader := proposalCacheInputReader(reader, o.Queries)
	if proposalInputsReader == nil {
		return nil, fmt.Errorf("ai: proposal cache input reader is unavailable")
	}
	vis, err = currentProposalCacheVisibility(ctx, proposalInputsReader, workspaceID, vis.ActorUserID)
	if err != nil {
		return nil, fmt.Errorf("ai: validate step-proposal workspace access: %w", err)
	}

	// ---- cache check ----
	// The HTTP/MCP caller may omit a SmartCreateReader when embeddings are
	// unavailable. The orchestrator still has the generated query handle in
	// production, which is needed to revalidate task inputs before serving a
	// cached response or returning a provider result.
	cacheReader := reader
	if cacheReader == nil {
		cacheReader, _ = o.Queries.(SmartCreateReader)
	}
	cacheKey := proposeStepsCacheKey(workspaceID, title, description, granularity, existingChildren, vis, TaskIDFromContext(ctx))
	var cached []ProposedTask
	if o.ProposalCache != nil {
		if snapshot, ok := o.ProposalCache.getEnvelope(cacheKey, &cached); ok {
			current, currentVis, cacheable, validateErr := currentProposalCacheSnapshot(ctx, proposalInputsReader, cacheReader, workspaceID, vis, proposalCacheResources(snapshot), false)
			if validateErr != nil {
				return nil, fmt.Errorf("ai: validate cached step-proposal inputs: %w", validateErr)
			}
			if cacheable && sameProposalVisibility(vis, currentVis) && sameProposalCacheSnapshot(snapshot, current) {
				return cached, nil
			}
			o.ProposalCache.Delete(cacheKey)
			vis = currentVis
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
					WorkspaceID: workspaceID,
					Model:       embedClient.Model(),
					TaskID:      0,
					IsElevated:  vis.IsElevated,
					ActorUserID: vis.ActorUserID,
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
						ranked = append(ranked, scoredTask{id: c.ID, publicID: c.PublicID.String(), title: c.Title, score: sim})
					}
					sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
					if len(ranked) > stepsTopN {
						ranked = ranked[:stepsTopN]
					}
				}
			}
		}
	}
	inputResources := make([]InvocationResource, 0, len(existingChildren)+len(ranked))
	for _, child := range existingChildren {
		inputResources = append(inputResources, TaskInputResource(child.PublicID))
	}
	for _, task := range ranked {
		inputResources = append(inputResources, TaskInputResource(task.publicID))
	}
	ctx = WithInputResources(ctx, inputResources...)
	rows, latestVis, _, loadErr := loadProposalCacheTaskInputs(ctx, proposalInputsReader, workspaceID, vis.ActorUserID, InputResourcesFromContext(ctx), true)
	if loadErr != nil {
		return nil, fmt.Errorf("ai: validate step-proposal inputs: %w", loadErr)
	}
	if !sameProposalVisibility(vis, latestVis) || !sameStepsPromptInputs(existingChildren, ranked, rows, TaskIDFromContext(ctx), title, description) {
		return nil, ErrProposalInputsChanged
	}
	cacheSnapshot := proposalCacheSnapshotFromRows(rows)
	vis = latestVis
	cacheKey = proposeStepsCacheKey(workspaceID, title, description, granularity, existingChildren, vis, TaskIDFromContext(ctx))
	cacheable := o.ProposalCache != nil

	// ---- build user prompt ----
	// Truncate user input before building the LLM prompt.
	userPrompt := buildStepsPrompt(sanitizeTitle(title), sanitizeDesc(description), existingChildren, ranked)

	// ---- call LLM ----
	req := airequest.New(prov, airequest.Args{
		System: proposeStepsSystemPrompt(granularity),
		Prompt: userPrompt,
		Output: stepsOutput,
	})
	var tasks []ProposedTask
	_, err = o.CompleteAndRecord(ctx, workspaceID, "propose_steps", prov, req, func(resp *providers.Response) error {
		payload, err := outputPayload(resp, stepsOutput)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &tasks); err != nil {
			return fmt.Errorf("%w: steps response could not be decoded", providers.ErrResponseSchemaMismatch)
		}
		minSteps, maxSteps := granularityRange(granularity)
		if err := validateProposedTasks(tasks, minSteps, maxSteps); err != nil {
			return err
		}
		return validateProposalCacheSnapshot(ctx, proposalInputsReader, cacheReader, workspaceID, vis.ActorUserID, cacheSnapshot, false)
	})
	if err != nil {
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	if cacheable {
		o.ProposalCache.putEnvelope(cacheKey, tasks, cacheSnapshot)
	}
	return tasks, nil
}

// proposeStepsCacheKey keys a decomposition on every prompt input: the
// actor's visibility (the similar tasks quoted are that actor's readable
// set) and the existing children (adding a subtask changes what must not
// be duplicated). The child count leads so the key stays unambiguous.
func proposeStepsCacheKey(workspaceID uint32, title, description string, granularity Granularity, children []ChildTaskSummary, vis acl.VisibilityArgs, subjectTaskID uint32) string {
	parts := make([]string, 0, 6+3*len(children))
	parts = append(parts, "propose_steps", VisibilityCacheScope(vis), title, description, string(granularity),
		"subject:"+strconv.FormatUint(uint64(subjectTaskID), 10),
		"children:"+strconv.Itoa(len(children)))
	for _, c := range children {
		parts = append(parts, c.PublicID, c.Title, c.State)
	}
	return ProposalCacheKey(workspaceID, parts...)
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
		TaskID:           TaskIDFromContext(ctx),
		Audience:         InvocationAudienceFromContext(ctx),
		InputResources:   InputResourcesFromContext(ctx),
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

func (o *Orchestrator) logFailure(ctx context.Context, workspaceID uint32, purpose string, req providers.Request, err error, decodedResponse ...*providers.Response) {
	if o.LogInvoke == nil {
		return
	}
	var response *providers.Response
	if len(decodedResponse) > 0 {
		response = decodedResponse[0]
	}
	model := req.Model
	if response != nil && response.Model != "" {
		model = response.Model
	}
	responseRedacted := Redact(err.Error())
	var tokensInput, tokensOutput int
	var costMicros, costCents int64
	if response != nil {
		responseRedacted = Redact(response.Text)
		tokensInput = response.InputTokens
		tokensOutput = response.OutputTokens
		costMicros = response.EstimatedCostMicros()
		costCents = response.EstimatedCostCents()
	}
	status := "error"
	if errors.Is(err, providers.ErrModelRefused) {
		status = "blocked"
	}
	o.LogInvoke(ctx, InvocationRecord{
		WorkspaceID:      workspaceID,
		AgentID:          AgentIDFromContext(ctx),
		TaskID:           TaskIDFromContext(ctx),
		Audience:         InvocationAudienceFromContext(ctx),
		InputResources:   InputResourcesFromContext(ctx),
		Purpose:          purpose,
		Model:            model,
		PromptRedacted:   Redact(strings.TrimSpace(req.System + "\n" + req.Prompt)),
		ResponseRedacted: responseRedacted,
		TokensInput:      tokensInput,
		TokensOutput:     tokensOutput,
		CostMicros:       costMicros,
		CostCents:        costCents,
		Status:           status,
		// Persist a bounded identifier rather than provider-controlled text:
		// ai_invocations.error_code is a small Latin-1 code column, and raw
		// messages can also contain secrets or non-ASCII response prose.
		ErrorCode: invocationErrorCode(err),
	})
}

func invocationErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrProposalInputsChanged):
		return "AI.PROPOSAL.INPUTS_CHANGED"
	case errors.Is(err, providers.ErrGenerationIncomplete):
		return "AI.RESPONSE.GENERATION_INCOMPLETE"
	case errors.Is(err, providers.ErrModelRefused):
		return "AI.RESPONSE.MODEL_REFUSED"
	case errors.Is(err, providers.ErrResponseInvalidJSON), errors.Is(err, ErrParse):
		return "AI.RESPONSE.INVALID_JSON"
	case errors.Is(err, providers.ErrResponseSchemaMismatch):
		return "AI.RESPONSE.SCHEMA_MISMATCH"
	case errors.Is(err, providers.ErrUpstreamRateLimited):
		return "AI.PROVIDER.UPSTREAM_RATE_LIMITED"
	case errors.Is(err, providers.ErrUpstreamTimeout):
		return "AI.PROVIDER.UPSTREAM_TIMEOUT"
	case errors.Is(err, providers.ErrUpstreamAuthRejected):
		return "AI.PROVIDER.UPSTREAM_AUTH_REJECTED"
	case errors.Is(err, providers.ErrUpstreamRequestRejected):
		return "AI.PROVIDER.UPSTREAM_REQUEST_REJECTED"
	case errors.Is(err, providers.ErrKeyDecryptFailed):
		return "AI.PROVIDER.KEY_DECRYPT_FAILED"
	case errors.Is(err, providers.ErrBaseURLDestinationNotAllowed), errors.Is(err, providers.ErrInvalidBaseURL):
		return "AI.PROVIDER.BASE_URL_NOT_ALLOWED"
	case errors.Is(err, providers.ErrBaseURLNotAllowed), errors.Is(err, providers.ErrUnknownKind), errors.Is(err, providers.ErrMissingKey), errors.Is(err, providers.ErrNoEnabledProvider):
		return "AI.PROVIDER.NOT_CONFIGURED"
	case errors.Is(err, providers.ErrUpstreamUnreachable):
		return "AI.PROVIDER.UPSTREAM_UNREACHABLE"
	default:
		return "AI.INVOCATION.FAILED"
	}
}
