// Package eventkinds reads this module's own sources and reports two
// things a compiler cannot: an event kind written as a string literal, and
// an event payload field carrying a row's internal id.
//
// Both are type-checking scans over the whole module rather than tests of
// any one package's behaviour, which is why they live under tests/ beside
// the other guards instead of next to the code they read. A gate names
// this package directly, so neither scan depends on being swept up by a
// whole-module test run to be enforced.
package eventkinds
