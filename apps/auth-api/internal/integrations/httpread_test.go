package integrations

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errReadPastCeiling is what a reader reports when it is asked for more
// bytes than a bounded read should ever consume.
var errReadPastCeiling = errors.New("read past the ceiling")

// tripwireReader serves `remaining` bytes of filler and then fails
// instead of ending.
//
// The failure is the point. A truncating check after the fact —
// "did we end up with more bytes than the ceiling?" — passes whether or
// not the read was bounded, because the surplus is discarded either
// way; what differs is how much of the endless response was pulled into
// memory first, and only the reader can see that. Failing on the read
// past the budget is what makes an unbounded read observable from a
// test.
type tripwireReader struct {
	remaining int
}

func (r *tripwireReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errReadPastCeiling
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func TestReadProviderBodyStopsAtTheCeiling(t *testing.T) {
	t.Parallel()

	// One byte past the ceiling is all a bounded read may consume: it is
	// what tells a body that ends at the ceiling from one that does not.
	body, err := readProviderBody(&tripwireReader{remaining: maxProviderResponseBytes + 1})

	require.ErrorIs(t, err, ErrResponseTooLarge,
		"a provider response past the ceiling must be reported, not silently truncated into a parse failure")
	assert.Len(t, body, maxProviderResponseBytes,
		"the read must stop at the ceiling instead of draining the provider's response into the auth-api heap")
}

func TestReadProviderBodyReturnsShortBodiesWhole(t *testing.T) {
	t.Parallel()

	const payload = `{"access_token":"abc"}`
	body, err := readProviderBody(strings.NewReader(payload))

	require.NoError(t, err)
	assert.Equal(t, payload, string(body),
		"a normal token envelope must survive the bounded read byte for byte")
}

// A body that lands exactly on the ceiling is complete, not truncated:
// the extra byte the reader asks for is what separates the two, and
// getting this boundary wrong would reject legitimate responses.
func TestReadProviderBodyAcceptsExactlyTheCeiling(t *testing.T) {
	t.Parallel()

	body, err := readProviderBody(io.LimitReader(&tripwireReader{remaining: maxProviderResponseBytes}, maxProviderResponseBytes))

	require.NoError(t, err)
	assert.Len(t, body, maxProviderResponseBytes)
}

func TestReadProviderBodyPropagatesReadErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("connection reset")
	_, err := readProviderBody(io.MultiReader(strings.NewReader("{"), errReader{sentinel}))

	require.ErrorIs(t, err, sentinel)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestErrorPreviewTruncatesLongBodies(t *testing.T) {
	t.Parallel()

	preview := errorPreview([]byte(strings.Repeat("y", maxProviderErrorPreviewBytes*3)))

	assert.Less(t, len(preview), maxProviderErrorPreviewBytes*2,
		"an upstream failure body must not be reproduced at full length inside an error string")
	assert.Contains(t, preview, "truncated",
		"a truncated preview must say so, or a reader cannot tell the body ended there")
}

func TestErrorPreviewKeepsShortBodiesIntact(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `{"error":"invalid_grant"}`, errorPreview([]byte(`{"error":"invalid_grant"}`)))
}

// Every provider in this package talks to a hard-coded URL, so there is
// no seam to inject an oversized response through and no way to assert
// the bound one call site at a time. This scan is what ties the call
// sites to the helper: it fails the moment a provider reads a response
// body without a ceiling, which is how all ten of them were written
// before.
func TestProvidersNeverReadAnUnboundedResponseBody(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "httpread.go" {
			continue
		}
		src, readErr := os.ReadFile(name) //#nosec G304 -- fixed glob over this package's own sources
		require.NoError(t, readErr)
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, "io.ReadAll(") {
				continue
			}
			assert.Contains(t, line, "io.LimitReader(",
				"%s:%d reads a response body without a ceiling; use readProviderBody or io.LimitReader", name, i+1)
		}
	}
}
