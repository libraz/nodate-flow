package mcp

import (
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog/mutationguard"
)

// mutationLogGates are the shared recorder's entry points, keyed by the
// method name a call site uses. Every one writes the audit row; all but
// RecordTxAudit additionally append the event.
var mutationLogGates = map[string]bool{
	"Record":        true,
	"RecordStrict":  true,
	"RecordInTx":    true,
	"RecordTxAudit": true,
}

// bannedRawAppends are the event-log entry points no file in this
// package may reach. A tool reaching one directly has appended the event
// and skipped the audit row, which is the half-recorded change the gates
// exist to prevent.
var bannedRawAppends = []string{
	"eventbus.Append",
	"eventbus.AppendBestEffort",
	"eventbus.AppendJudgeEvent",
	"eventbus.AppendReverseEvent",
}

// bannedImports keeps the audit recorder out of this package's import
// set, so a call to a gate name can only be the mutation log's, which is
// what makes the name-based reachability check mean what it says.
var bannedImports = []string{
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit",
}

// eventOptionalGates is the gate whose event another writer already
// appended, so a mutation handed to it names no kind and every other one
// must.
var eventOptionalGates = map[string]bool{"RecordTxAudit": true}

// writeToolsWithoutMutationLog lists the write-scoped tools that record
// nothing because they change nothing a person could later find missing.
//
// write:workspace is not a synonym for "mutates": the scope also covers
// spending the workspace's AI budget, because a read-only token's holder
// cannot undo a charge (see the scope vocabulary on session.hasScope).
// Every entry below is in the scope for that second reason, so the
// justification each one needs is that it writes no workspace state — not
// that it is harmless.
//
// The map is an allowlist rather than documentation: a newly registered
// write:workspace tool is absent from it and therefore fails
// [TestMCPMutatingToolsRecordBothHalves] until it either routes through
// a mutation-log gate or is added here with the reason it persists
// nothing.
var writeToolsWithoutMutationLog = map[string]string{
	"propose_tasks_from": "asks the model for task candidates and returns them; persists nothing",
	"propose_priority":   "asks the model for a priority and returns it; persists nothing",
	"propose_steps":      "asks the model for a task breakdown and returns it; apply_steps is what persists",
	"propose_lens":       "compiles a query into a lens definition and returns it; persists nothing",
	"propose_duplicates": "similarity search; the only write is the task's own embedding, a derived cache",
	"propose_relations":  "similarity search; the only write is the task's own embedding, a derived cache",
}

// readToolsRequiringMutationLog names the read-scoped tools that must
// still leave a trace. A read is not automatically uninteresting: an
// export takes a workspace's task data out in bulk, which is precisely
// the event an administrator investigating a leak needs to find, and it
// is the operation an automated caller reaches for most.
var readToolsRequiringMutationLog = map[string]string{
	"export_tasks": "bulk extraction of task data; REST records the same export",
}

// analyzeMCPPackage parses this package for the structural checks below.
// Selector calls count, because the tools reach the recorder as a method;
// the banned import above is what keeps an unrelated method of the same
// name from satisfying the check.
func analyzeMCPPackage(t *testing.T) *mutationguard.Analysis {
	t.Helper()
	a, err := mutationguard.Load(".")
	if err != nil {
		t.Fatalf("analyse this package: %v", err)
	}
	return a
}

// TestMCPMutatingToolsRecordBothHalves walks the registered tool table
// and proves every tool that changes something, or takes something out
// in bulk, reaches the one place that records it.
//
// Driven by the registry rather than by a list of functions on purpose:
// the audit that prompted this found three tools missing the record and
// the fix for three tools is not a fix, because the fourth is written
// later by somebody who never read this file.
func TestMCPMutatingToolsRecordBothHalves(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	if len(h.tools) == 0 {
		t.Fatal("no tools registered")
	}
	analysis := analyzeMCPPackage(t)
	registry := mcpRegisteredTools(t)

	seenExempt := map[string]bool{}
	seenRequiredRead := map[string]bool{}
	for name, tl := range h.tools {
		required := tl.requiredScope == ScopeWriteWorkspace
		if _, ok := readToolsRequiringMutationLog[name]; ok {
			required = true
			seenRequiredRead[name] = true
		}
		if !required {
			continue
		}
		if _, exempt := writeToolsWithoutMutationLog[name]; exempt {
			seenExempt[name] = true
			continue
		}
		entry := mcpRunFuncName(t, registry, name)
		if !analysis.Reaches(entry, mutationLogGates) {
			t.Errorf("tool %q (%s) changes the workspace but never reaches a mutation-log gate (%s); "+
				"route it through the shared recorder (RecordInTx inside the write's transaction, or "+
				"RecordTxAudit when a shared transactional helper already appended the event), or add it to "+
				"writeToolsWithoutMutationLog with the reason it persists nothing",
				name, entry, strings.Join(mutationguard.SortedKeys(mutationLogGates), " / "))
		}
	}

	for name := range writeToolsWithoutMutationLog {
		if !seenExempt[name] {
			t.Errorf("writeToolsWithoutMutationLog lists %q, which is no longer a registered write:workspace tool; drop the stale entry", name)
		}
	}
	for name := range readToolsRequiringMutationLog {
		if !seenRequiredRead[name] {
			t.Errorf("readToolsRequiringMutationLog lists %q, which is no longer a registered tool; drop the stale entry", name)
		}
	}
}

// TestMCPEventbusAppendCentralized proves no tool reaches around the
// shared recorder to the event bus or the audit recorder.
//
// Without this half, the reachability test above is satisfiable by a
// tool that appends its event directly and skips the audit row — which
// is the exact state most of these tools were in.
func TestMCPEventbusAppendCentralized(t *testing.T) {
	t.Parallel()

	a := analyzeMCPPackage(t)
	for _, finding := range a.Centralized("", bannedRawAppends) {
		t.Error(finding.String())
	}
	for _, finding := range a.Imports(bannedImports) {
		t.Error(finding.String())
	}
}

// TestMCPMutationLiteralsNameBothHalves reads every mutation literal in
// the package and proves it names the audit action, and — except at the
// one gate that exists for changes whose event a shared transactional
// helper already appended — the event kind too.
//
// A mutation with one half filled in compiles, runs, and records half
// the change. Half is worse than none: the table that got a row reads
// as a complete answer to whoever queries it.
func TestMCPMutationLiteralsNameBothHalves(t *testing.T) {
	t.Parallel()

	findings, examined := analyzeMCPPackage(t).Literals(mutationguard.LiteralSpec{
		TypeName:           "mutationlog.Mutation",
		Gates:              mutationLogGates,
		Required:           []string{"AuditAction"},
		EventOptionalGates: eventOptionalGates,
		EventField:         "EventType",
	})
	for _, finding := range findings {
		t.Error(finding.String())
	}
	if examined == 0 {
		t.Fatal("no mutation literals found; the guard is passing because it is looking at nothing")
	}
}
