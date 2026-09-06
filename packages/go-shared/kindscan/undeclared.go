package kindscan

import (
	"strconv"
	"sync"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// UndeclaredFuncName is the fully qualified name of [Undeclared], as the
// type checker spells it. [Scan] matches on this rather than on the
// shape of the call, so the escape is one named function and not a
// pattern anybody can reproduce by accident.
const UndeclaredFuncName = "github.com/libraz/nodate-flow/packages/go-shared/kindscan.Undeclared"

// Undeclared builds an event kind out of a string that is deliberately
// not one of the declared constants.
//
// Some tests have to construct a kind no constant names. A guard that
// rejects an unknown kind needs an unknown kind to reject; an assertion
// that filters a timeline by kind needs one nothing else emits, or the
// count it asserts is only true until the next handler starts emitting
// the kind it chose. Neither can be written with a constant, because the
// value not existing is the point.
//
// Writing the literal directly would work — Go converts it implicitly —
// and that is exactly what [Scan] forbids, because it cannot tell that
// literal apart from a kind somebody invented at a call site that meant
// to emit a real one. So the two cases are made to look different: this
// function is the sanctioned way to say "not a declared kind", it reads
// as that at the call site, and it greps as that across the tree.
//
// It refuses a value that is declared. An escape that accepted
// "task.created" would launder the very literal the guard exists to
// find, so a declared kind panics here and [Scan] reports the same call
// statically, without waiting for the test to run.
//
// The static check sees constant arguments only. A kind assembled at run
// time still panics, one test run later.
func Undeclared(s string) eventbus.Kind {
	if IsDeclaredKind(s) {
		panic("kindscan.Undeclared: " + strconv.Quote(s) +
			" is a declared event kind; use the constant from packages/go-shared/eventbus")
	}
	return eventbus.Kind(s)
}

// declared is the set behind [IsDeclaredKind], built once.
var declared = sync.OnceValue(func() map[eventbus.Kind]struct{} {
	kinds := eventbus.Kinds()
	set := make(map[eventbus.Kind]struct{}, len(kinds))
	for _, k := range kinds {
		set[k] = struct{}{}
	}
	return set
})

// IsDeclaredKind reports whether s is one of the kinds
// packages/go-shared/eventbus declares.
//
// Kinds minted at run time by eventbus.TaskTransition are not declared
// and are not covered here; nothing needs [Undeclared] to build one.
func IsDeclaredKind(s string) bool {
	_, ok := declared()[eventbus.Kind(s)]
	return ok
}
