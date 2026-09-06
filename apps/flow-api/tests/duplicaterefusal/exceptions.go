package duplicaterefusal

import (
	"os"
	"path/filepath"
	"strings"
)

// AttributionException is a branch this package accepts it cannot place,
// together with the reason it cannot.
//
// It exists so the derivation can say "there is a refusal here whose write
// I did not resolve" out loud and still be usable, rather than choosing
// between a permanent failure and a silent gap. It is not a statement that
// the branch is correct: nothing here says the table carries a collidable
// key, only that no reader of this file can tell which table it is.
//
// It is keyed by file and function rather than by line, so it survives the
// code around it moving and dies with the function it names. An entry that
// covers no branch is refused for the same reason a stale marker is: a
// reader finds a site named and accounted for, when the site may simply
// have gone.
type AttributionException struct {
	// File is the repository-relative file the branch sits in.
	File string
	// Func is the package-qualified function, as [Branch.Func] spells it,
	// so the entry covers one function rather than the whole file.
	Func string
	// Reason states why the write cannot be resolved. It is mandatory.
	Reason string
}

// AttributionExceptions are the branches whose write this package cannot
// name.
var AttributionExceptions = []AttributionException{}

// Covers reports whether the exception is about this branch.
func (e AttributionException) Covers(b Branch) bool {
	return b.File == e.File && b.Func == e.Func
}

// Problem returns what is wrong with the exception itself, given the
// branches the derivation could not place, or the empty string when the
// entry is sound.
//
// The three ways an entry rots are all failures: a file that is not in the
// repository, a reason that says nothing, and an entry that covers no
// unresolved branch. The last is the one that matters most — it is what an
// exemption becomes once the code it excused is fixed or deleted, and it
// reads exactly like an exemption somebody still relies on.
func (e AttributionException) Problem(root string, unresolved []Unresolved) string {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(e.File))); err != nil {
		return "names " + e.File + ", which is not a file in this repository"
	}
	if strings.TrimSpace(e.Reason) == "" {
		return "states no reason, and the reason is the whole content of the entry"
	}
	for _, u := range unresolved {
		if e.Covers(u.Branch) {
			return ""
		}
	}
	return "covers no branch this check failed to place; the site it was written for has been " +
		"moved, rewritten or resolved, so drop it"
}

// ExceptionFor returns the exception covering a branch, or nil.
func ExceptionFor(b Branch) *AttributionException {
	for i := range AttributionExceptions {
		if AttributionExceptions[i].Covers(b) {
			return &AttributionExceptions[i]
		}
	}
	return nil
}
