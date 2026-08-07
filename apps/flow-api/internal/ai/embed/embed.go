// Package embed provides the task embedding client used by the
// duplicate-detection pipeline (ADR 0003). It normalizes vectors from a
// pluggable Provider, hashes the source text to skip redundant work,
// and upserts rows into task_embeddings via the sqlc-generated query
// layer.
//
// Vectors are stored as MySQL 9.x VECTOR columns; the write/read path
// uses STRING_TO_VECTOR / VECTOR_TO_STRING so the wire format is the
// textual "[0.1,0.2,...]" form. Cosine similarity is a dot product on
// the unit-length vectors, computed in Go (MySQL 9.6 Community does not
// ship VEC_DISTANCE_COSINE).
package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// Dim is the default embedding dimensionality (see ADR 0003).
const Dim = 768

// Provider produces a raw embedding for a piece of text. Implementations
// need not normalize; the Client applies L2 normalization before store.
type Provider interface {
	// Model returns the opaque model key written to task_embeddings.model.
	Model() string
	// Embed returns a vector of length Dim for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Store is the sqlc query subset Client needs. generated.Queries
// satisfies it in production; tests can inject a narrow fake.
type Store interface {
	GetTaskEmbedding(ctx context.Context, arg generated.GetTaskEmbeddingParams) (generated.GetTaskEmbeddingRow, error)
	UpsertTaskEmbedding(ctx context.Context, arg generated.UpsertTaskEmbeddingParams) error
}

// CostGuard is the narrow budget-gate contract used by Client.
type CostGuard interface {
	Check(ctx context.Context, workspaceID uint32) error
}

// InvocationMetricsHook is called after each embedding provider call.
type InvocationMetricsHook func(provider, model, workspaceID string, costMicros int64)

// InvocationLogger persists a redacted embedding invocation record.
type InvocationLogger func(ctx context.Context, rec InvocationRecord)

// InvocationRecord mirrors the ai_invocations payload without importing
// the parent ai package, which would create an import cycle.
type InvocationRecord struct {
	WorkspaceID      uint32
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

// Redactor scrubs task text before it is written to ai_invocations.
type Redactor func(string) string

// Client upserts task embeddings through a Provider. It is safe for
// concurrent use.
type Client struct {
	Provider Provider
	Queries  Store

	Guard        CostGuard
	LogInvoke    InvocationLogger
	OnInvocation InvocationMetricsHook
	Redact       Redactor
}

// Model returns the key every task_embeddings row this client writes is
// stored under, and therefore the only key a reader may look them up by.
//
// Readers must call this rather than resolving a model name of their
// own. They used to read ai_settings.embed_model, a column with no write
// path and a default of "mock-768", while writes went in under the
// provider's real model name — so duplicate detection matched nothing at
// all on any deployment with a real embedding provider, and matched
// everything on the mock the tests ran against.
//
// Nil-safe: a workspace with no embedder has no embeddings, and the
// empty key it returns matches none.
func (c *Client) Model() string {
	if c == nil || c.Provider == nil {
		return ""
	}
	return c.Provider.Model()
}

// New constructs a Client. Panics if provider or queries is nil so
// wiring mistakes fail loudly at boot.
func New(provider Provider, q Store) *Client {
	if provider == nil || q == nil {
		panic("embed.New: provider and queries must be non-nil")
	}
	return &Client{Provider: provider, Queries: q}
}

// WithMetering wires budget enforcement, metrics, and audit logging for
// embedding provider calls. It returns c for compact startup wiring.
func (c *Client) WithMetering(guard CostGuard, log InvocationLogger, onInvocation InvocationMetricsHook, redact Redactor) *Client {
	c.Guard = guard
	c.LogInvoke = log
	c.OnInvocation = onInvocation
	c.Redact = redact
	return c
}

// EmbedTask generates and upserts the embedding for a task. If the
// (task_id, model) row already exists with the same content hash the
// call is a no-op, so repeated PATCHes that don't change title or
// description stay cheap.
//
// workspaceID is denormalized into task_embeddings.workspace_id so the
// duplicate-detection query layer can scope by workspace without joining
// through tasks. It MUST match the owning task's workspace_id; the FK on
// task_embeddings.workspace_id enforces that the row exists in workspaces.
func (c *Client) EmbedTask(ctx context.Context, workspaceID, taskID uint32, title, description string) error {
	text := composeTaskText(title, description)
	if text == "" {
		return nil
	}
	hash := hashText(text)
	model := c.Model()

	existing, err := c.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
		TaskID: taskID,
		Model:  model,
	})
	if err == nil && existing.ContentHash == hash {
		return nil
	}
	if c.Guard != nil {
		if err := c.Guard.Check(ctx, workspaceID); err != nil {
			return err
		}
	}

	raw, err := c.Provider.Embed(ctx, text)
	if err != nil {
		c.recordMetrics(workspaceID, 0)
		c.logFailure(ctx, workspaceID, text, err)
		return fmt.Errorf("provider embed: %w", err)
	}
	if len(raw) == 0 {
		err := errors.New("embed: provider returned empty vector")
		c.recordMetrics(workspaceID, 0)
		c.logFailure(ctx, workspaceID, text, err)
		return err
	}
	Normalize(raw)

	if err := c.Queries.UpsertTaskEmbedding(ctx, generated.UpsertTaskEmbeddingParams{
		TaskID:         taskID,
		WorkspaceID:    workspaceID,
		Model:          model,
		Dim:            uint16(len(raw)), //#nosec G115 -- embedding dimensions cap at the upstream model's MaxDim (~3072 today), well below uint16
		StringToVector: Encode(raw),
		ContentHash:    hash,
	}); err != nil {
		return err
	}
	costMicros := estimateCostMicros(model, text)
	c.recordMetrics(workspaceID, costMicros)
	c.logSuccess(ctx, workspaceID, text, costMicros)
	return nil
}

func (c *Client) recordMetrics(workspaceID uint32, costMicros int64) {
	if c.OnInvocation == nil {
		return
	}
	c.OnInvocation(providerName(c.Provider), c.Model(), strconv.FormatUint(uint64(workspaceID), 10), costMicros)
}

func (c *Client) logSuccess(ctx context.Context, workspaceID uint32, text string, costMicros int64) {
	if c.LogInvoke == nil {
		return
	}
	c.LogInvoke(ctx, InvocationRecord{
		WorkspaceID:      workspaceID,
		Purpose:          "embed_task",
		Model:            c.Model(),
		PromptRedacted:   c.redact(text),
		ResponseRedacted: "embedding vector omitted",
		TokensInput:      estimateTokens(text),
		CostMicros:       costMicros,
		CostCents:        costMicros / 10_000,
		Status:           "ok",
	})
}

func (c *Client) logFailure(ctx context.Context, workspaceID uint32, text string, err error) {
	if c.LogInvoke == nil {
		return
	}
	c.LogInvoke(ctx, InvocationRecord{
		WorkspaceID:    workspaceID,
		Purpose:        "embed_task",
		Model:          c.Model(),
		PromptRedacted: c.redact(text),
		Status:         "error",
		ErrorCode:      c.redact(err.Error()),
	})
}

func (c *Client) redact(s string) string {
	if c.Redact != nil {
		return c.Redact(s)
	}
	return s
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return max(1, (len(text)+3)/4)
}

func estimateCostMicros(model, text string) int64 {
	return providers.EstimateCostMicrosUSD(model, estimateTokens(text), 0)
}

type namedProvider interface {
	ProviderName() string
}

func providerName(p Provider) string {
	if named, ok := p.(namedProvider); ok {
		return named.ProviderName()
	}
	return "embed"
}

// Normalize rescales v in place to unit length (L2). A zero vector is
// left untouched.
func Normalize(v []float32) {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// Encode serializes v to the STRING_TO_VECTOR-compatible text form.
func Encode(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 12)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// Decode parses the VECTOR_TO_STRING output back into a float slice.
// Whitespace between elements is tolerated.
func Decode(s []byte) ([]float32, error) {
	text := strings.TrimSpace(string(s))
	text = strings.TrimPrefix(text, "[")
	text = strings.TrimSuffix(text, "]")
	if text == "" {
		return nil, nil
	}
	parts := strings.Split(text, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, fmt.Errorf("decode vector: %w", err)
		}
		out = append(out, float32(f))
	}
	return out, nil
}

// Cosine returns the dot product of two unit-length vectors. Lengths
// must match; otherwise the function returns 0.
func Cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func composeTaskText(title, description string) string {
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
		return title + "\n\n" + description
	}
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
