package outbounddeadline

import (
	"strings"
	"testing"
)

// TestEveryOutboundCallCarriesADeadline reads the workspace and refuses a
// request that can leave this repository and never come back.
//
// Nothing is listed here. The files come out of the tree, the installers
// out of the bodies that perform an install, and the calls that read their
// client from a context out of what each file imports — so a client
// written tomorrow, and a helper renamed tomorrow, are both covered
// without anyone remembering they exist.
func TestEveryOutboundCallCarriesADeadline(t *testing.T) {
	t.Parallel()

	scan := load(t)

	for _, f := range scan.Unexempted() {
		switch f.Kind {
		case SharedDefaultClient:
			t.Errorf("%s: %s uses %s, which carries no deadline: a peer that accepts the "+
				"connection and then stops answering holds this goroutine for as long as it "+
				"keeps the socket open, with no error to return and nothing to log. Give the "+
				"call a client with a Timeout, or say here why it cannot hang: %s",
				f.Location(), describe(f), f.What, MarkerForm)
		case ClientWithoutDeadline:
			t.Errorf("%s: %s builds an http.Client with no Timeout. Every request through it "+
				"waits indefinitely; set Timeout, or say here why it cannot hang: %s",
				f.Location(), describe(f), MarkerForm)
		case ContextWithoutClient:
			t.Errorf("%s: %s calls %s with a context nothing installed an HTTP client into, so "+
				"the library falls back to http.DefaultClient and the call has no deadline. "+
				"Nothing at the call site says so, which is why it is checked here. Pass a "+
				"context from authn.WithOutboundHTTPClient (or oidc.ClientContext directly), "+
				"or say why it cannot hang: %s",
				f.Location(), describe(f), f.What, MarkerForm)
		case StaleMarker:
			// Reported by its own test, which says why.
		}
	}
}

// TestOutboundMarkersStillApply drops an exemption that has outlived what
// it excused.
//
// A marker is a claim that one call cannot hang. Once the call is gone the
// claim is not about anything, and a reader who finds it there concludes
// something was considered and cleared.
func TestOutboundMarkersStillApply(t *testing.T) {
	t.Parallel()

	for _, f := range load(t).Findings {
		if f.Kind != StaleMarker {
			continue
		}
		t.Errorf("%s: this outbound-deadline marker covers no call. It exempts nothing and "+
			"reads as though something was checked; drop it", f.Location())
	}
}

// load runs the scan and refuses a result that would pass because it found
// nothing.
//
// Every assertion above is of the form "nothing was reported", so each
// half of the derivation has to be shown to have matched something: the
// file walk, the client-literal shape, the library calls that read their
// client out of a context, the installs that excuse them, and the marker
// pattern. A rename in any one of them would otherwise turn this whole
// package into a test that passes by looking at nothing.
func load(t *testing.T) Scan {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	scan, err := ScanRepository(root)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}

	if scan.Files == 0 {
		t.Fatal("no Go source file was read; the scan is looking at nothing")
	}
	if scan.Clients == 0 {
		t.Fatal("no http.Client literal was found anywhere in the workspace; the client shape " +
			"has stopped matching rather than every client having gone away")
	}
	if scan.ContextCalls == 0 {
		t.Fatal("no call that reads its HTTP client out of a context was found; the go-oidc " +
			"and oauth2 call vocabulary has stopped matching rather than sign-in having been removed")
	}
	if len(scan.Installers) == 0 {
		t.Fatal("no function in the workspace installs an HTTP client into a context; the " +
			"install derivation has stopped matching, and with it every exemption it grants")
	}
	if scan.Markers == 0 {
		t.Fatalf("no outbound-deadline marker was found in the workspace. Either the marker "+
			"pattern has stopped matching what is written in the tree — an em dash replaced by "+
			"a hyphen does it — or every exemption really is gone. The form is: %s", MarkerForm)
	}
	return scan
}

// describe names the function a finding sits in, or the file when it sits
// at package level.
func describe(f Finding) string {
	if f.Function != "" {
		return f.Function
	}
	return strings.TrimSuffix(f.File[strings.LastIndex(f.File, "/")+1:], ".go")
}
