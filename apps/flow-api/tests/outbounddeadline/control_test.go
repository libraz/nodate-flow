package outbounddeadline

import (
	"fmt"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// controlSource is the known-bad tree, written next to its nearest
// legitimate neighbour in every case: a client with a deadline beside one
// without, a discovery call that was handed a client beside one that was
// not, a marker with a reason beside a marker with none.
//
// Every line the scan must report carries `// want: <Kind>` and every line
// it must stay silent about does not, so the expected set is read out of
// the source rather than written twice.
const controlSource = `package p

import (
	"context"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// deadlined is what a client is supposed to look like.
var deadlined = &http.Client{Timeout: 5 * time.Second}

var bare = &http.Client{} // want: ClientWithoutDeadline

var written = &http.Client{Timeout: 0} // want: ClientWithoutDeadline

var routed = &http.Client{Transport: http.DefaultTransport} // want: ClientWithoutDeadline

// A transport is not a client, and its own timeouts are not the one that
// bounds a whole request.
var transport = &http.Transport{IdleConnTimeout: time.Minute}

func reachesForTheDefault(req *http.Request) {
	_, _ = http.DefaultClient.Do(req) // want: SharedDefaultClient
	_, _ = http.Get("https://example.test") // want: SharedDefaultClient
	_, _ = http.Post("https://example.test", "application/json", nil) // want: SharedDefaultClient
	_, _ = deadlined.Do(req)
	_ = http.DefaultTransport
}

func discoversWithNothingInstalled(ctx context.Context) {
	_, _ = oidc.NewProvider(ctx, "https://issuer.example") // want: ContextWithoutClient
}

func discoversWithOneInstalled(ctx context.Context) {
	_, _ = oidc.NewProvider(withClient(ctx), "https://issuer.example")
}

func discoversThroughABinding(ctx context.Context) {
	ctx = withClient(ctx)
	_, _ = oidc.NewProvider(ctx, "https://issuer.example")
}

func discoversThroughAWrappedBinding(ctx context.Context) {
	fetchCtx, cancel := context.WithTimeout(withClient(ctx), time.Second)
	defer cancel()
	_, _ = oidc.NewProvider(fetchCtx, "https://issuer.example")
}

func discoversThroughAnotherPackagesInstaller(ctx context.Context) {
	_, _ = oidc.NewProvider(traced(ctx), "https://issuer.example")
}

func exchangesWithNothingInstalled(ctx context.Context, cfg *oauth2.Config, code string) {
	_, _ = cfg.Exchange(ctx, code) // want: ContextWithoutClient
}

func exchangesWithOneInstalled(ctx context.Context, cfg *oauth2.Config, code string) {
	_, _ = cfg.Exchange(withClient(ctx), code)
}

// stated says why its call cannot hang.
//
// outbound-deadline: not-applicable — this one is exempt and the next one is not.
func stated(ctx context.Context) {
	_, _ = oidc.NewProvider(ctx, "https://issuer.example")
}

// reasonless carries a marker with nothing after it.
//
// outbound-deadline: not-applicable —
func reasonless(ctx context.Context) {
	_, _ = oidc.NewProvider(ctx, "https://issuer.example") // want: ContextWithoutClient
}

// discussed talks about the exemption: a call like this would need an
// outbound-deadline: not-applicable comment above it.
func discussed(req *http.Request) {
	_, _ = http.DefaultClient.Do(req) // want: SharedDefaultClient
}

// spare covers a call that is not there.
//
// outbound-deadline: not-applicable — this function makes no outbound call.
func spare() {}
`

// installerSource is a second file, so the installer closure is exercised
// across files the way it is in the repository: authn defines the install
// and auth-api reaches it by name.
const installerSource = `package p

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
)

// withClient installs the deadline-bearing client.
func withClient(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, deadlined)
}

// traced reaches the install through withClient.
func traced(ctx context.Context) context.Context {
	return withClient(ctx)
}

// borrows returns no context, so installing inside it hands the caller
// nothing and it is not an installer.
func borrows(ctx context.Context) error {
	_ = withClient(ctx)
	return nil
}
`

// TestScanSeesEachShape is the positive control.
//
// Every assertion in the repository test is of the form "nothing was
// found", and a scan that has stopped scanning satisfies all of them. So
// each shape is fed in here deliberately, together with the nearest thing
// that must stay silent.
func TestScanSeesEachShape(t *testing.T) {
	t.Parallel()

	scan := scanControl(t)

	want := expectedFromSource(controlSource)
	got := map[int]string{}
	for _, f := range scan.Findings {
		if f.Marked || f.Kind == StaleMarker || f.File != "control.go" {
			continue
		}
		got[f.Line] = kindName(f.Kind)
	}
	if !sameLines(got, want) {
		t.Errorf("the scan reported %s, want %s", render(got), render(want))
	}
}

// TestTheMarkerExcusesOneSiteAndOnlyAStatedOne pins what an exemption is
// worth: it covers the site below it, a marker that states no reason is
// not one, a sentence about the marker is not one, and a marker that
// covers nothing is reported rather than ignored.
func TestTheMarkerExcusesOneSiteAndOnlyAStatedOne(t *testing.T) {
	t.Parallel()

	scan := scanControl(t)

	var exempted, stale []int
	for _, f := range scan.Findings {
		switch {
		case f.Kind == StaleMarker:
			stale = append(stale, f.Line)
		case f.Marked:
			exempted = append(exempted, f.Line)
		}
	}
	if len(exempted) != 1 {
		t.Errorf("the scan exempted %d sites, want exactly the one under the stated marker", len(exempted))
	}
	if len(stale) != 1 {
		t.Errorf("the scan reported %d stale markers, want the one covering a function that "+
			"makes no outbound call", len(stale))
	}
	if scan.Markers != 2 {
		t.Errorf("the scan found %d markers, want 2: the reasonless one and the discussed one "+
			"must not count", scan.Markers)
	}
}

// TestTheInstallIsWhatExcuses pins the half of the check that reads
// dataflow: the library calls are excused by a client having been put into
// the context they were handed, not by the enclosing function having
// mentioned one somewhere.
func TestTheInstallIsWhatExcuses(t *testing.T) {
	t.Parallel()

	const src = `package p

import (
	"context"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var client = &http.Client{Timeout: 1}

func installsDirectlyAtTheCall(ctx context.Context) {
	_, _ = oidc.NewProvider(oidc.ClientContext(ctx, client), "https://issuer.example")
}

func installsUnderTheOauthKey(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

func reachesTheOauthKeyInstaller(ctx context.Context) {
	_, _ = oidc.NewProvider(installsUnderTheOauthKey(ctx), "https://issuer.example")
}

func installsAndThenDiscardsIt(ctx context.Context) {
	_ = oidc.ClientContext(ctx, client)
	_, _ = oidc.NewProvider(ctx, "https://issuer.example") // want: ContextWithoutClient
}

func installsOnADifferentContext(ctx context.Context, other context.Context) {
	other = oidc.ClientContext(other, client)
	_ = other
	_, _ = oidc.NewProvider(ctx, "https://issuer.example") // want: ContextWithoutClient
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "install.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	scan := ScanSources(fset, []Source{{Name: "install.go", File: file}})

	got := map[int]string{}
	for _, f := range scan.Unexempted() {
		got[f.Line] = kindName(f.Kind)
	}
	if want := expectedFromSource(src); !sameLines(got, want) {
		t.Errorf("the scan reported %s, want %s", render(got), render(want))
	}
	if len(scan.Installers) == 0 {
		t.Error("no installer was derived from a file that defines one; the install derivation " +
			"has stopped matching and every library call would be reported")
	}
}

// TestTheInstallerClosureStopsAtFunctionsThatReturnNoContext keeps the
// name-keyed closure from excusing calls it never touched. A function that
// installs into something of its own hands its caller no context, so
// calling it proves nothing about the context the caller then passes.
func TestTheInstallerClosureStopsAtFunctionsThatReturnNoContext(t *testing.T) {
	t.Parallel()

	scan := scanControl(t)
	installers := map[string]bool{}
	for _, name := range scan.Installers {
		installers[name] = true
	}
	for _, want := range []string{"withClient", "traced"} {
		if !installers[want] {
			t.Errorf("%s installs a client and returns a context; it should be an installer", want)
		}
	}
	if installers["borrows"] {
		t.Error("borrows returns no context, so calling it hands the caller nothing; " +
			"treating it as an installer would excuse every call named borrows in the tree")
	}
}

func scanControl(t *testing.T) Scan {
	t.Helper()

	fset := token.NewFileSet()
	sources := make([]Source, 0, 2)
	for name, src := range map[string]string{
		"control.go":   controlSource,
		"installer.go": installerSource,
	} {
		file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		sources = append(sources, Source{Name: name, File: file})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return ScanSources(fset, sources)
}

// expectedFromSource reads the `// want: <Kind>` annotations out of a
// control source, so the expectation lives on the line it is about.
func expectedFromSource(src string) map[int]string {
	out := map[int]string{}
	for i, line := range strings.Split(src, "\n") {
		_, after, found := strings.Cut(line, "// want:")
		if !found {
			continue
		}
		out[i+1] = strings.TrimSpace(after)
	}
	return out
}

func sameLines(got, want map[int]string) bool {
	if len(got) != len(want) {
		return false
	}
	for line, kind := range want {
		if got[line] != kind {
			return false
		}
	}
	return true
}

func render(m map[int]string) string {
	lines := make([]int, 0, len(m))
	for line := range m {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, fmt.Sprintf("%d:%s", line, m[line]))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " ")
}

func kindName(k FindingKind) string {
	switch k {
	case SharedDefaultClient:
		return "SharedDefaultClient"
	case ClientWithoutDeadline:
		return "ClientWithoutDeadline"
	case ContextWithoutClient:
		return "ContextWithoutClient"
	case StaleMarker:
		return "StaleMarker"
	}
	return "unknown"
}
