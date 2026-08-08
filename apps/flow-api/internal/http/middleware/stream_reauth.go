package middleware

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// ErrStreamUnauthorized is what [StreamReauthorizer] returns when the
// re-check fails. The code carried on it is the one the gate itself
// wrote, so a caller can log the same code a fresh request would have
// been answered with.
type ErrStreamUnauthorized struct {
	// Status is the HTTP status the gate produced (401, 403, 404).
	Status int
	// Code is the error code from the problem+json body, empty when the
	// gate wrote no recognisable body.
	Code string
}

func (e *ErrStreamUnauthorized) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("stream: no longer authorized (status %d)", e.Status)
	}
	return fmt.Sprintf("stream: no longer authorized (%s)", e.Code)
}

// StreamReauthorizer turns the middleware chain guarding a route into a
// check that can be re-run on a connection that is already open.
//
// A long-lived stream is authorized once, when it opens, and then keeps
// delivering for as long as the client holds the socket. Everything the
// gate decided — that the token is valid and unexpired, that it is
// bound to this workspace, that its owner is still a member — is a fact
// about the moment of connection, and none of it is re-examined when it
// stops being true. Removing somebody from a workspace or revoking
// their token left their open stream receiving every event in the
// workspace, which is a read of the whole workspace and is exactly what
// the revocation was for.
//
// Rather than restate those rules, this replays the chain itself
// against the original request with a throwaway response writer. There
// is one gate, and a stream stays open only while it keeps passing the
// same one a fresh request would.
//
// The chain must be the same middlewares mounted on the route, in the
// same order. Nothing is written to the real client: responses go to
// the discarding writer, which only records the status and body the
// chain produced.
func StreamReauthorizer(chain ...func(http.Handler) http.Handler) func(*http.Request) error {
	return func(r *http.Request) error {
		// The terminal handler is built per call so two connections
		// re-checking at the same time cannot see each other's result.
		passed := false
		var handler http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			passed = true
		})
		// Wrap outermost-last so the chain runs in mount order.
		for i := len(chain) - 1; i >= 0; i-- {
			if chain[i] == nil {
				continue
			}
			handler = chain[i](handler)
		}

		rec := &captureWriter{header: make(http.Header)}
		handler.ServeHTTP(rec, r)
		if passed {
			return nil
		}
		status := rec.status
		if status == 0 {
			status = http.StatusForbidden
		}
		return &ErrStreamUnauthorized{Status: status, Code: rec.problemCode()}
	}
}

// captureWriter is an http.ResponseWriter that keeps the status and
// body in memory instead of sending them anywhere. The re-check runs
// the real gate, and the real gate writes an error response on
// failure; that response belongs to nobody — the client is already
// mid-stream — so it is captured and read for its code.
type captureWriter struct {
	header http.Header
	status int
	body   []byte
}

func (c *captureWriter) Header() http.Header { return c.header }

func (c *captureWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.body = append(c.body, p...)
	return len(p), nil
}

// Hijack refuses rather than handing over the live connection: nothing
// in an authorization chain should be upgrading a request that is only
// being re-checked, and silently succeeding would take the socket out
// from under the stream.
func (c *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("middleware: re-authorization writer cannot be hijacked")
}

// problemCode reads the error code out of the captured problem+json
// body. An unrecognisable body yields an empty string, which the caller
// reports as a bare status.
func (c *captureWriter) problemCode() string {
	if len(c.body) == 0 {
		return ""
	}
	var doc struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(c.body, &doc); err != nil {
		return ""
	}
	return doc.Type
}
