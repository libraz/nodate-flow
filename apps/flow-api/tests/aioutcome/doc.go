// Package aioutcome derives, from the AI sources themselves, the LLM
// provider calls whose outcome nothing records.
//
// Every call into a provider is counted once, on the path it actually
// took: the invocation metrics hook is notified on the success path with
// no error and on the failure path with the provider's error. A call
// that notifies on only one of the two paths is not a smaller bug than
// one that notifies on neither — the provider was invoked, the tokens
// were spent, and the metric simply reads lower. Under-counting looks
// like less traffic rather than like a defect, so nothing downstream
// ever contradicts it, and the failure rate derived from those counters
// is wrong in the direction that hides trouble.
//
// The shape is copied from call site to call site, which is how the
// omission spreads: the branch that returns early is the one that gets
// written first and the one nobody rereads.
//
//	reach set  the functions that can reach the invocation hook,
//	           computed to a fixpoint: a function that calls a hook
//	           value named OnInvocation, or that calls another scanned
//	           function already in the set.
//	           Nothing here is a list of call sites or helper names — a
//	           helper that starts recording tomorrow enters the set on
//	           its own, and so does every caller of it.
//	omission   a provider call bound to an error that is not branched
//	           on, or branched on with a path that reaches no hook. The
//	           chain by which the other path reaches is reported with
//	           it, because the fix belongs at whichever hop the silent
//	           path is missing. A call whose two paths are both silent
//	           is one finding rather than two: the pair reads as a
//	           contradiction about the same line, and the answer is
//	           simply that nothing records anything.
//	mislabel   a hook-reaching call that passes a literal nil in its
//	           trailing error position from inside an error check. That
//	           records a failure as a success, which is worse than not
//	           recording it: the error rate reads as zero rather than as
//	           low.
//
// The branch the failure takes is read off the check rather than
// assumed. A condition may compare the error against nil either way
// round and may carry further operands — a retry that insists on a
// non-nil response as well — and the two branches swap with the sense.
// A condition that reaches the comparison two ways at once is reported
// without naming a side, because sending the fix to the branch that was
// already right costs more than saying which one was not read.
//
// The mislabel rule is lexical on purpose. It asks only what a call
// syntactically inside an error branch passes, and never follows the
// argument into the callee, so a helper that forwards its own error
// parameter is not a finding — that helper's own call sites are where
// the value is chosen.
//
// An error check is recognised only where the identifier compared
// against nil is known to hold an error without type information: it is
// declared error in a signature or a var declaration, or it is the
// trailing destination of a multi-value assignment from a call. A nil
// check on anything else is left alone, because a cache-hit branch
// recording a legitimate success reads identically otherwise.
//
// The providers package is not scanned. It implements the provider
// interface rather than calling it, so its own Complete and Embed
// methods are the callee at every site this checks. Test files are not
// scanned either: a test that calls a provider is exercising the
// provider rather than spending a workspace's budget, and there is no
// invocation hook behind it to notify.
//
// The scan proves it scanned something before it reports success: a walk
// that has stopped matching, a root that moved, or a parse that silently
// returned nothing all produce a clean run otherwise. Every root has to
// yield Go files, provider calls and hook-reaching functions, and the
// counts are printed on every run.
package aioutcome
