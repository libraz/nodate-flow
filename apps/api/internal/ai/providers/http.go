package providers

import (
	"net/http"
	"time"
)

// defaultHTTPTimeout is the per-request upstream LLM timeout. Long enough
// for slow generations, short enough that a stuck call cannot pin a
// goroutine forever.
const defaultHTTPTimeout = 90 * time.Second

// sharedClient is the package-wide *http.Client. We reuse one transport so
// keep-alives work; per-call timeouts come from the request context.
var sharedClient = &http.Client{Timeout: defaultHTTPTimeout}

// zero overwrites b with zero bytes. Used to scrub plaintext API keys after
// they have been written to an outbound Authorization header.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
