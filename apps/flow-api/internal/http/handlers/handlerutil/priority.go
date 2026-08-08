package handlerutil

import "strings"

// Priority bounds for the tasks.priority column. 0 means "no priority",
// which is the column default and a legitimate value — not a missing
// one.
const (
	PriorityNone = int32(0)
	PriorityMax  = int32(4)
)

// PriorityFromLabel maps the priority label an LLM answers with onto the
// integer scale the tasks table and every task DTO use. The second
// return value reports whether the label was in the vocabulary.
//
// The models are asked for a word because a word is what they produce
// reliably; everything downstream of the proposal — the DTO, the DB
// column, the UI badge — speaks the 0..4 scale. Somewhere in between,
// the two have to meet, and the only place that can be is the server:
// leaving the label on the wire pushed the mapping onto every client,
// where it existed once, as a lookup that silently substituted a
// mid-range default for any word it did not recognise. A client cannot
// discover the mapping from the OpenAPI document, so a proposal could
// not be applied without guessing.
//
// The vocabulary is the one the prompt asks for — low, medium, high —
// and nothing wider. An unknown word resolves to PriorityNone, which is
// the column default and the honest reading of a value the vocabulary
// does not contain. It is reported rather than absorbed: a model
// answering outside the vocabulary it was given is a fact about the
// model, and a caller that turns it into a number without a word about
// it makes a malformed answer indistinguishable from a deliberate one.
// That is what the client-side map did, and nobody would have known.
func PriorityFromLabel(label string) (int32, bool) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "low":
		return 1, true
	case "medium":
		return 2, true
	case "high":
		return 3, true
	case "":
		// An omitted priority is not a failed one: the model was not
		// obliged to rank every step it proposed.
		return PriorityNone, true
	default:
		return PriorityNone, false
	}
}
