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

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
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

// Client upserts task embeddings through a Provider. It is safe for
// concurrent use.
type Client struct {
	Provider Provider
	Queries  *generated.Queries
}

// New constructs a Client. Panics if provider or queries is nil so
// wiring mistakes fail loudly at boot.
func New(provider Provider, q *generated.Queries) *Client {
	if provider == nil || q == nil {
		panic("embed.New: provider and queries must be non-nil")
	}
	return &Client{Provider: provider, Queries: q}
}

// EmbedTask generates and upserts the embedding for a task. If the
// (task_id, model) row already exists with the same content hash the
// call is a no-op, so repeated PATCHes that don't change title or
// description stay cheap.
func (c *Client) EmbedTask(ctx context.Context, taskID uint32, title, description string) error {
	text := composeTaskText(title, description)
	if text == "" {
		return nil
	}
	hash := hashText(text)
	model := c.Provider.Model()

	existing, err := c.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
		TaskID: taskID,
		Model:  model,
	})
	if err == nil && existing.ContentHash == hash {
		return nil
	}

	raw, err := c.Provider.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("provider embed: %w", err)
	}
	if len(raw) == 0 {
		return errors.New("embed: provider returned empty vector")
	}
	Normalize(raw)

	return c.Queries.UpsertTaskEmbedding(ctx, generated.UpsertTaskEmbeddingParams{
		TaskID:         taskID,
		Model:          model,
		Dim:            uint16(len(raw)),
		StringToVector: Encode(raw),
		ContentHash:    hash,
	})
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
