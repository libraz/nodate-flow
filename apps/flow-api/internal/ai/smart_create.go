package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// --------------------------------------------------------------------
// Interfaces
// --------------------------------------------------------------------

// EmbedClient is the narrow contract ProposeSmartCreate uses to produce
// an embedding vector for the new task's text and to obtain the model
// name for querying existing embeddings.
type EmbedClient interface {
	// Embed returns a normalized vector for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Model returns the model key stored in task_embeddings.model.
	Model() string
}

// SmartCreateReader is the narrow contract ProposeSmartCreate uses to
// fetch task assignee and workspace member information for the LLM
// prompt. The methods are satisfied by *generated.Queries.
type SmartCreateReader interface {
	ListCandidateTaskEmbeddings(ctx context.Context, arg generated.ListCandidateTaskEmbeddingsParams) ([]generated.ListCandidateTaskEmbeddingsRow, error)
	ListTasksWithAssigneesForSmartCreate(ctx context.Context, arg generated.ListTasksWithAssigneesForSmartCreateParams) ([]generated.ListTasksWithAssigneesForSmartCreateRow, error)
	ListWorkspaceMembersForSmartCreate(ctx context.Context, workspaceID uint32) ([]generated.ListWorkspaceMembersForSmartCreateRow, error)
}

// --------------------------------------------------------------------
// Response types
// --------------------------------------------------------------------

// SmartProposal is the LLM-generated proposal for a new task, including
// suggested assignees for the parent task and a subtask breakdown with
// per-subtask assignee suggestions.
type SmartProposal struct {
	SuggestedAssignees []AssigneeSuggestion `json:"suggestedAssignees"`
	Subtasks           []SubtaskProposal    `json:"subtasks"`
}

// AssigneeSuggestion is a single assignee recommendation with a
// confidence score and a brief rationale referencing similar past tasks.
type AssigneeSuggestion struct {
	UserPublicID string  `json:"userPublicId"`
	DisplayName  string  `json:"displayName"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
}

// SubtaskProposal is a single subtask the LLM suggests for the parent
// task, including an optional assignee recommendation.
type SubtaskProposal struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Priority    string              `json:"priority"`
	Assignee    *AssigneeSuggestion `json:"assignee,omitempty"`
}

// --------------------------------------------------------------------
// Constants
// --------------------------------------------------------------------

// scoredTask pairs a task's internal ID and title with its cosine
// similarity score against the query vector.
type scoredTask struct {
	id    uint32
	title string
	score float32
}

const (
	// smartCreateMaxCandidates is the number of candidate embeddings to
	// retrieve from the database for cosine comparison.
	smartCreateMaxCandidates = 200

	// smartCreateTopN is the number of most-similar tasks included in
	// the LLM prompt after cosine ranking.
	smartCreateTopN = 10

	// smartCreateTopAssigneeN is the maximum number of task IDs fetched
	// with assignee information for prompt enrichment.
	smartCreateTopAssigneeN = 20
)

const smartCreateSystemPrompt = "You are a project planning assistant for the nodate-flow workspace.\n" +
	"Given a high-level task and historical context about similar past tasks,\n" +
	"break it into concrete subtasks and suggest assignees based on who\n" +
	"handled similar work before.\n" +
	"\n" +
	"Reply ONLY with a JSON object:\n" +
	"{\n" +
	"  \"suggestedAssignees\": [\n" +
	"    {\"userPublicId\": \"...\", \"displayName\": \"...\", \"confidence\": 0.0-1.0, \"reason\": \"...\"}\n" +
	"  ],\n" +
	"  \"subtasks\": [\n" +
	"    {\n" +
	"      \"title\": \"...\",\n" +
	"      \"description\": \"...\",\n" +
	"      \"priority\": \"low\" | \"medium\" | \"high\",\n" +
	"      \"assignee\": {\"userPublicId\": \"...\", \"displayName\": \"...\", \"confidence\": 0.0-1.0, \"reason\": \"...\"}\n" +
	"    }\n" +
	"  ]\n" +
	"}\n" +
	"\n" +
	"Rules:\n" +
	"- Return ONLY valid JSON, no prose, no markdown fences.\n" +
	"- suggestedAssignees: top candidates for the parent task (max 3).\n" +
	"- subtasks: 2-7 concrete execution steps.\n" +
	"- priority: low, medium, or high.\n" +
	"- assignee: best candidate for each subtask. Omit if no clear match.\n" +
	"- confidence: your estimate of how well the member matches (0.0-1.0).\n" +
	"- reason: brief explanation referencing similar past tasks."

// --------------------------------------------------------------------
// ProposeSmartCreate
// --------------------------------------------------------------------

// ProposeSmartCreate asks the workspace's default LLM provider to
// decompose a task into subtasks and suggest assignees. The method
// embeds the new task text, finds the most similar existing tasks via
// cosine similarity, enriches the LLM prompt with assignee history and
// workspace members, and returns a structured proposal.
//
// Both the prompt and the response are redacted before logging. The
// cost guard is checked before any external call.
func (o *Orchestrator) ProposeSmartCreate(
	ctx context.Context,
	workspaceID uint32,
	title, description string,
	embedClient EmbedClient,
	scReader SmartCreateReader,
) (*SmartProposal, error) {
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
	cacheKey := ProposalCacheKey(workspaceID, "smart_create", title, description)
	if cached, ok := o.ProposalCache.Get(cacheKey); ok {
		if sp, _ := cached.(*SmartProposal); sp != nil {
			return sp, nil
		}
	}

	// Truncate user input before embedding and LLM prompt building to
	// limit prompt-injection surface and prevent prompt-size abuse.
	title = sanitizeTitle(title)
	description = sanitizeDesc(description)

	// ---- embed the new task text ----
	taskText := composeText(title, description)
	queryVec, err := embedClient.Embed(ctx, taskText)
	if err != nil {
		return nil, fmt.Errorf("ai: smart create embed failed: %w", err)
	}
	embed.Normalize(queryVec)

	// ---- retrieve candidate embeddings ----
	candidates, err := scReader.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
		WorkspaceID: workspaceID,
		Model:       embedClient.Model(),
		TaskID:      0, // no self to exclude
		Limit:       smartCreateMaxCandidates,
	})
	if err != nil {
		return nil, fmt.Errorf("ai: smart create list candidates: %w", err)
	}

	// ---- cosine rank ----
	ranked := make([]scoredTask, 0, len(candidates))
	for _, c := range candidates {
		vec, derr := embed.Decode(toBytes(c.Vector))
		if derr != nil || len(vec) != len(queryVec) {
			continue
		}
		sim := embed.Cosine(queryVec, vec)
		ranked = append(ranked, scoredTask{id: c.ID, title: c.Title, score: sim})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > smartCreateTopAssigneeN {
		ranked = ranked[:smartCreateTopAssigneeN]
	}

	// ---- fetch assignees for top tasks ----
	var taskIDs []uint32
	for _, s := range ranked {
		taskIDs = append(taskIDs, s.id)
	}

	var taskRows []generated.ListTasksWithAssigneesForSmartCreateRow
	if len(taskIDs) > 0 {
		taskRows, err = scReader.ListTasksWithAssigneesForSmartCreate(ctx, generated.ListTasksWithAssigneesForSmartCreateParams{
			WorkspaceID: workspaceID,
			TaskIds:     taskIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("ai: smart create list assignees: %w", err)
		}
	}

	// ---- fetch workspace members ----
	members, err := scReader.ListWorkspaceMembersForSmartCreate(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("ai: smart create list members: %w", err)
	}

	// ---- build user prompt ----
	userPrompt := buildSmartCreatePrompt(title, description, ranked, taskRows, members)

	// ---- call LLM ----
	req := providers.Request{
		System: smartCreateSystemPrompt,
		Prompt: userPrompt,
	}
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, 0)
		o.logFailure(ctx, workspaceID, "propose_smart_create", req, err)
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, wsIDStr, resp.EstimatedCostMicros())
	o.logSuccess(ctx, workspaceID, "propose_smart_create", req, resp)

	// ---- parse response ----
	payload := extractJSON(resp.Text)
	if payload == "" {
		return nil, ErrParse
	}
	var proposal SmartProposal
	if err := json.Unmarshal([]byte(payload), &proposal); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	o.ProposalCache.Put(cacheKey, &proposal)
	return &proposal, nil
}

// composeText joins title and description with a newline separator,
// trimming whitespace. An empty result means there is nothing to embed.
func composeText(title, description string) string {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	switch {
	case title == "" && description == "":
		return ""
	case description == "":
		return title
	case title == "":
		return description
	default:
		return title + "\n" + description
	}
}

// buildSmartCreatePrompt assembles the user prompt from the new task,
// similar historical tasks with assignees, and workspace members.
func buildSmartCreatePrompt(
	title, description string,
	ranked []scoredTask,
	taskRows []generated.ListTasksWithAssigneesForSmartCreateRow,
	members []generated.ListWorkspaceMembersForSmartCreateRow,
) string {
	var b strings.Builder

	// New task
	b.WriteString("## New Task\n")
	b.WriteString("Title: ")
	b.WriteString(title)
	b.WriteByte('\n')
	if description != "" {
		b.WriteString("Description: ")
		b.WriteString(description)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	// Similar past tasks (top N)
	if len(ranked) > 0 {
		b.WriteString("## Similar Past Tasks\n")

		// Build assignee lookup: taskID -> list of (name, publicID)
		type assigneeInfo struct {
			name     string
			publicID string
		}
		assigneeMap := make(map[uint32][]assigneeInfo)
		for _, r := range taskRows {
			if r.AssigneeName.Valid {
				assigneeMap[r.ID] = append(assigneeMap[r.ID], assigneeInfo{
					name:     r.AssigneeName.String,
					publicID: r.AssigneePublicID.String(),
				})
			}
		}

		limit := smartCreateTopN
		if len(ranked) < limit {
			limit = len(ranked)
		}
		for i := 0; i < limit; i++ {
			s := ranked[i]
			fmt.Fprintf(&b, "- \"%s\" (similarity: %.2f)", s.title, s.score)
			if aa, ok := assigneeMap[s.id]; ok && len(aa) > 0 {
				names := make([]string, len(aa))
				for j, a := range aa {
					names[j] = fmt.Sprintf("%s [%s]", a.name, a.publicID)
				}
				fmt.Fprintf(&b, " assigned to: %s", strings.Join(names, ", "))
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	// Workspace members
	if len(members) > 0 {
		b.WriteString("## Workspace Members\n")
		for _, m := range members {
			fmt.Fprintf(&b, "- %s [%s]\n", m.DisplayName, m.UserPublicID.String())
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// toBytes converts the interface{} value returned by VECTOR_TO_STRING
// into a []byte for embed.Decode.
func toBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}
