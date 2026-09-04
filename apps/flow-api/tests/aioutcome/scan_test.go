package aioutcome

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNoProviderCallGoesUnrecorded refuses an LLM provider call whose
// outcome nothing records.
//
// A call into a provider has already spent its tokens and its latency by
// the time it returns, so both of its paths are invocations and both are
// counted. Recording only the success is not half a bug: the counter
// reads lower, which is indistinguishable from less traffic, and the
// failure rate derived from it is wrong in the direction that looks
// calm. Recording the failure as a success is worse still — the rate
// reads as zero.
//
// `go test` throws away a passing package's output, so the target that
// runs this passes -v: the counts below are the evidence that the scan
// read anything, and they are worth nothing unseen.
func TestNoProviderCallGoesUnrecorded(t *testing.T) {
	t.Parallel()

	result := scanRepository(t)
	for _, c := range result.Counts {
		t.Log(c.String())
	}
	for _, f := range result.Findings {
		if f.Chain == nil {
			t.Errorf("%s: %s", f.Location(), f.Explain())
			continue
		}
		t.Errorf("%s: %s. The other path reaches the hook by %s",
			f.Location(), f.Explain(), f.ChainString())
	}
}

// TestScanSeesAProviderCallThatRecordsOnOnePath is the positive control,
// and it runs on every invocation: without it a checker that has stopped
// matching reports a clean tree, which is the same output as a clean
// tree.
//
// The sample pins the fixpoint rather than a one-level walk — one call
// site records through a helper that records through another helper, and
// it must not be flagged — and pins what is not a finding: a call that
// is not into a provider, a helper forwarding the error it was handed,
// and a nil check on something that is not an error. It pins the
// collapse too: the call that records on neither path is one finding,
// because two would read as a contradiction about the same line. And it
// pins the forward search with its limit — a latency bound between the
// call and the check is not a finding, an error overwritten before the
// check is.
func TestScanSeesAProviderCallThatRecordsOnOnePath(t *testing.T) {
	t.Parallel()

	const src = `package p

// record notifies the invocation hook.
func (s *service) record(ctx context.Context, model string, err error) {
	s.hooks.OnInvocation(ctx, model, err)
}

// forward records through record, one hop further from the hook.
func (s *service) forward(ctx context.Context, model string, err error) {
	s.record(ctx, model, err)
}

// buildRequest touches no hook at all.
func buildRequest(model string) request {
	return request{Model: model}
}

// completeBothPaths records the failure and the success.
func (s *service) completeBothPaths(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, req)
	if err != nil {
		s.hooks.OnInvocation(ctx, req.Model, err)
		return "", err
	}
	s.hooks.OnInvocation(ctx, req.Model, nil)
	return out, nil
}

// completeThroughTwoHops records through forward, which records through
// record. Neither hop mentions the hook by name.
func (s *service) completeThroughTwoHops(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, buildRequest(req.Model))
	if err != nil {
		s.forward(ctx, req.Model, err)
		return "", err
	}
	s.forward(ctx, req.Model, nil)
	return out, nil
}

// completeOmitsFailure hands the provider's error back without counting
// the call that produced it.
func (s *service) completeOmitsFailure(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, req) // want: omission
	if err != nil {
		return "", err
	}
	s.hooks.OnInvocation(ctx, req.Model, nil)
	return out, nil
}

// completeCompoundFailureCheck takes the failure branch on an empty
// response as well as on an error, so the check is wider than the error
// alone.
func (s *service) completeCompoundFailureCheck(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, req)
	if err != nil || out == "" {
		s.hooks.OnInvocation(ctx, req.Model, err)
		return "", err
	}
	s.hooks.OnInvocation(ctx, req.Model, nil)
	return out, nil
}

// completeInvertedWithElse checks the error the other way round: the
// success runs in the body and the failure in the else.
func (s *service) completeInvertedWithElse(ctx context.Context, req request) (string, error) {
	retryOut, retryErr := s.provider.Complete(ctx, req)
	if retryErr == nil && retryOut != "" {
		s.hooks.OnInvocation(ctx, req.Model, nil)
		return retryOut, nil
	} else {
		s.hooks.OnInvocation(ctx, req.Model, retryErr)
		return "", retryErr
	}
}

// completeInvertedOmitsFailure records inside the branch the success
// takes and leaves the other one silent.
func (s *service) completeInvertedOmitsFailure(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, req) // want: inverted omission
	if err == nil {
		s.hooks.OnInvocation(ctx, req.Model, nil)
		return out, nil
	}
	return "", err
}

// completeRecordsNeither spends an invocation that is invisible in the
// counters whichever way it went.
func (s *service) completeRecordsNeither(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, req) // want: neither
	if err != nil {
		return "", err
	}
	return out, nil
}

// completeMeasuresElapsed binds the latency between the call and the
// check, which leaves the outcome exactly where it was.
func (s *service) completeMeasuresElapsed(ctx context.Context, req request) (string, error) {
	start := time.Now()
	out, err := s.provider.Complete(ctx, req)
	elapsed := time.Since(start)
	s.observe(elapsed)
	if err != nil {
		s.hooks.OnInvocation(ctx, req.Model, err)
		return "", err
	}
	s.hooks.OnInvocation(ctx, req.Model, nil)
	return out, nil
}

// completeReassignsError overwrites the provider's error before anything
// checks it, so the branch below decides on a later call's outcome and
// the provider's own is never told apart.
func (s *service) completeReassignsError(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, req) // want: reassigned
	out, err = s.postProcess(out)
	if err != nil {
		s.hooks.OnInvocation(ctx, req.Model, err)
		return "", err
	}
	s.hooks.OnInvocation(ctx, req.Model, nil)
	return out, nil
}

// embedMislabelsFailure counts both paths and calls both of them a
// success.
func (s *service) embedMislabelsFailure(ctx context.Context, req request) ([]float32, error) {
	vec, err := s.provider.Embed(ctx, req)
	if err != nil {
		s.record(ctx, req.Model, nil) // want: mislabel
		return nil, err
	}
	s.record(ctx, req.Model, nil)
	return vec, nil
}

// loadCached calls no provider, and the success it records on a cache
// hit is one it really had.
func (s *service) loadCached(ctx context.Context, req request) (string, error) {
	hit, err := s.cache.Lookup(ctx, req)
	if err != nil {
		return "", err
	}
	if hit != nil {
		s.record(ctx, req.Model, nil)
		return hit.Text, nil
	}
	return "", nil
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}
	findings, reach, callsByDir := analyze(fset, []Package{{
		Dir:   "sample",
		Files: []File{{Rel: "sample.go", AST: file}},
	}})

	if _, ok := reach[funcKey{dir: "sample", name: "forward"}]; !ok {
		t.Error("forward reaches the hook through record, so the fixpoint must hold it")
	}
	if _, ok := reach[funcKey{dir: "sample", name: "buildRequest"}]; ok {
		t.Error("buildRequest reaches no hook, so it must stay out of the set")
	}
	if got, want := callsByDir["sample"], 10; got != want {
		t.Errorf("counted %d provider calls in the sample, want %d", got, want)
	}

	var flagged []string
	for _, f := range findings {
		flagged = append(flagged, f.String())
	}
	want := []string{
		fmt.Sprintf("sample.go:%d: %s", lineOf(t, src, "// want: omission"), KindFailureUnrecorded),
		fmt.Sprintf("sample.go:%d: %s", lineOf(t, src, "// want: inverted omission"), KindFailureUnrecorded),
		fmt.Sprintf("sample.go:%d: %s", lineOf(t, src, "// want: neither"), KindNeitherRecorded),
		fmt.Sprintf("sample.go:%d: %s", lineOf(t, src, "// want: reassigned"), KindUnchecked),
		fmt.Sprintf("sample.go:%d: %s", lineOf(t, src, "// want: mislabel"), KindMislabel),
	}
	if !slices.Equal(flagged, want) {
		t.Errorf("flagged %v, want %v", flagged, want)
	}
}

// TestScanLeavesTestFilesAlone pins the scope of the rule against the
// walk rather than against a parse, because that is where the exclusion
// has to hold: a test file that is read and then ignored still inflates
// the file count, and an inflated count is what the non-emptiness proof
// is trusting.
//
// The two files carry the same fault. Only the production one is an
// accounting fault: the other is exercising the provider, has no
// workspace paying for it and no invocation hook behind it to notify.
func TestScanLeavesTestFilesAlone(t *testing.T) {
	t.Parallel()

	const body = `package svc

func (s *service) record(ctx context.Context, model string, err error) {
	s.hooks.OnInvocation(ctx, model, err)
}

func (s *service) complete(ctx context.Context, req request) (string, error) {
	out, err := s.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	s.record(ctx, req.Model, nil)
	return out, nil
}
`

	root := t.TempDir()
	pkgDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("create sample tree: %v", err)
	}
	for _, name := range []string{"svc.go", "svc_test.go"} {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	result, err := Scan(root, []string{"svc"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := result.CheckNonEmpty(); err != nil {
		t.Fatalf("the production file alone must satisfy the non-emptiness proof: %v", err)
	}

	var flagged []string
	for _, f := range result.Findings {
		flagged = append(flagged, f.String())
	}
	want := []string{fmt.Sprintf("svc/svc.go:%d: %s", lineOf(t, body, "s.provider.Complete"), KindFailureUnrecorded)}
	if !slices.Equal(flagged, want) {
		t.Errorf("flagged %v, want %v", flagged, want)
	}

	counts := result.Counts[0]
	if counts.GoFiles != 1 {
		t.Errorf("counted %d Go files, want the one that is not a test", counts.GoFiles)
	}
	if counts.ProviderCalls != 1 {
		t.Errorf("counted %d provider calls, want the one that is not in a test", counts.ProviderCalls)
	}
}

// TestScanRefusesARootThatYieldedNothing pins the non-emptiness proof. A
// checker pointed somewhere with no provider call in it reports no
// findings, which is indistinguishable from a tree that records every
// outcome unless the scan says so itself.
func TestScanRefusesARootThatYieldedNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pkgDir := filepath.Join(root, "empty", "pkg")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("create sample tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "x.go"), []byte("package pkg\n"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	result, err := Scan(root, []string{"empty"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("a tree with no provider call cannot yield findings, got %d", len(result.Findings))
	}
	err = result.CheckNonEmpty()
	if err == nil {
		t.Fatal("a root with no provider call must fail the scan rather than pass it")
	}
	if !strings.Contains(err.Error(), "no provider call") {
		t.Errorf("the failure must name what was missing, got: %v", err)
	}
}

// scanRepository runs the scan against the real tree and refuses a
// verdict from a scan that read nothing.
func scanRepository(t *testing.T) Result {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	result, err := Scan(root, Roots)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := result.CheckNonEmpty(); err != nil {
		t.Fatalf("%v", err)
	}
	return result
}

// lineOf returns the line of the one sample statement carrying a marker,
// so the expectations name the fault rather than a line number that
// moves the next time the sample is edited.
func lineOf(t *testing.T, src, marker string) int {
	t.Helper()

	line := 0
	seen := 0
	for i, text := range strings.Split(src, "\n") {
		if strings.Contains(text, marker) {
			seen++
			line = i + 1
		}
	}
	if seen != 1 {
		t.Fatalf("marker %q appears %d times in the sample, want it exactly once", marker, seen)
	}
	return line
}
