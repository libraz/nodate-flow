// Package goroutinefail derives, from the test sources themselves, the
// goroutines that can fail a test from the wrong goroutine.
//
// testing.T.FailNow and testing.T.SkipNow — and with them every
// require.*, t.Fatal, t.Fatalf and t.Skip — may only be called from the
// goroutine running the test. Called
// anywhere else they run runtime.Goexit on a goroutine the test framework
// is not waiting on, so the failure is either silently lost or, once the
// test has already returned, raised as a panic that takes the whole test
// binary down with it. The suites most exposed to this are the ones that
// race concurrent requests, which is exactly where a flake reads as a
// product bug and a product bug reads as a flake.
//
// A goroutine that needs to report a failure therefore carries the
// outcome back as data — a channel, a results slice, a struct field —
// and the test goroutine asserts on it. That rule is only worth stating
// if it is enforced, because the offending call is usually not written
// in the goroutine body: it sits several hops down a shared helper, and
// a helper acquires a require the day someone tightens it.
//
//	reach set  the package-level functions that can reach a Goexit,
//	           computed to a fixpoint: a function that calls require.*,
//	           t.Fatal*, t.FailNow, t.Skip*, or any function already in
//	           the set.
//	           Nothing here is a list of helper names — a helper that
//	           starts requiring tomorrow enters the set on its own, and
//	           so does every caller of it.
//	finding    a `go` statement in a test file whose body reaches that
//	           set, directly or through one of those functions. The
//	           chain by which it reaches is reported with it, because
//	           the fix is at whichever hop is willing to return an error.
//
// assert.*, t.Errorf, t.Logf and t.Helper are not FailNow. Reporting a
// failure with t.Errorf from another goroutine is explicitly permitted
// and is the intended alternative when carrying the outcome back would
// be contrived, so it is not flagged.
//
// The scan proves it scanned something before it reports success: a
// walk that has stopped matching, a root that moved, or a parse that
// silently returned nothing all produce a clean run otherwise. Every
// root has to yield test files, `go` statements and FailNow-reaching
// functions, and the counts are printed on every run.
package goroutinefail
