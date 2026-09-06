package mentions

import (
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// The notation a mention is written in inside a body:
//
//	@[Display Name](user:019649b0-0000-7000-8000-000000000000)
//
// Only the id is resolved. The display name between the brackets is
// decoration the author typed and the renderer shows; nothing reads it.
//
// A bare @name is deliberately not a mention. Resolving one would mean
// guessing between two members who chose the same display name, and the
// mention would stop naming the same person the moment either of them
// renamed. Pinning the id is what keeps a mention correct across both.
const (
	openMarker  = "@["
	idMarker    = "](user:"
	closeMarker = ')'
)

// Extract returns the user public ids body names, in first-appearance
// order and de-duplicated: naming the same person twice in one body is
// one mention.
//
// Anything that is not a complete, parseable notation is skipped in
// silence — a marker that never reaches `](user:`, a notation with no
// closing parenthesis, an id that is not a UUID. A body is prose a person
// typed, so a half-written mention is an ordinary thing to find in one
// and not a reason to refuse the write it belongs to.
//
// The display name may hold `]` and `)`: the id marker is the first
// `](user:` after the opening one, so brackets inside the name are
// carried along rather than ending it. A second `@[` before that marker
// is read as a nearer notation opening, which is what makes a mention
// typed after an abandoned marker still resolve.
//
// Markdown structure is not interpreted. A notation inside a fenced
// block or an inline code span is extracted like any other, so a body
// documenting the notation mentions whoever the example names. Honouring
// fences would mean parsing markdown here, and the backend never does —
// the structure the reader sees is produced by the frontend, and a
// second parser that disagreed with it would decide mentions by rules
// nobody can see on screen.
func Extract(body string) []types.PublicID {
	var ids []types.PublicID
	seen := make(map[types.PublicID]struct{})
	for i := 0; i < len(body); {
		open := strings.Index(body[i:], openMarker)
		if open < 0 {
			break
		}
		nameStart := i + open + len(openMarker)
		mid := strings.Index(body[nameStart:], idMarker)
		if mid < 0 {
			// No id marker remains anywhere, so no later notation can be
			// completed either.
			break
		}
		if inner := strings.Index(body[nameStart:nameStart+mid], openMarker); inner >= 0 {
			// The marker being scanned never terminated; a nearer one opens
			// inside what would have been its display name.
			i = nameStart + inner
			continue
		}
		idStart := nameStart + mid + len(idMarker)
		end := strings.IndexByte(body[idStart:], closeMarker)
		if end < 0 {
			// Same reasoning as the id marker: every remaining notation
			// would have to close after this point.
			break
		}
		id, err := types.Parse(body[idStart : idStart+end])
		if err != nil {
			// Resume just past the opening marker rather than past the whole
			// candidate, so an unparseable id cannot hide a well-formed
			// notation that starts inside it.
			i = nameStart
			continue
		}
		i = idStart + end + 1
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}
