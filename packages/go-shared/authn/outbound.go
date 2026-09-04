package authn

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OutboundTimeout bounds one call to an identity provider: the discovery
// document, the JWKS fetch behind id_token verification, and the token
// exchange.
//
// It has to be stated because neither library states one. go-oidc and
// golang.org/x/oauth2 both fall back to http.DefaultClient when the
// context carries no client of its own, and http.DefaultClient has no
// deadline at all — a provider that accepts the connection and then stops
// answering holds the sign-in goroutine for as long as it keeps the socket
// open.
const OutboundTimeout = 15 * time.Second

// DefaultRetryAfter is how long [Discovery] answers from a cached failure
// before it attempts discovery again.
const DefaultRetryAfter = 30 * time.Second

// outboundClient is the one client every OIDC and OAuth2 call in this
// repository goes through. One client, so the transport's connection pool
// is reused across sign-ins.
var outboundClient = &http.Client{Timeout: OutboundTimeout}

// WithOutboundHTTPClient returns ctx carrying the deadline-bearing client
// that the OIDC and OAuth2 libraries must use.
//
// Installing it is not optional and it is not visible at the call site:
// oidc.NewProvider, oauth2.Config.Exchange and IDTokenVerifier.Verify all
// take a context and silently reach for http.DefaultClient when it holds
// no client. oidc.ClientContext stores the client under the key oauth2
// reads, so one install covers discovery, the JWKS fetch and the token
// exchange alike.
func WithOutboundHTTPClient(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, outboundClient)
}

// Discovery runs OIDC provider discovery on demand and keeps the result.
//
// Discovery is the only part of sign-in that talks to the issuer before a
// user is involved, and two properties of it decide whether an unreachable
// issuer is an outage:
//
//   - A failure is not permanent. Holding the error for the life of the
//     process turns one unreachable moment at boot into sign-in being down
//     until somebody restarts the binary, with nothing to say why.
//   - A retry is not per request. Attempts are serialised on one mutex and
//     a failure is answered from cache for RetryAfter, so an issuer that is
//     down collects one attempt per cooldown rather than one per sign-in
//     and a slow one has at most a single request in flight.
//
// The zero value is ready to use.
type Discovery struct {
	// RetryAfter is how long a failed attempt is answered from the cached
	// error. Zero means [DefaultRetryAfter].
	RetryAfter time.Duration
	// Now is the clock the cooldown is measured on. Nil means time.Now.
	Now func() time.Time

	mu        sync.Mutex
	built     bool
	lastErr   error
	lastErrAt time.Time
}

// Do ensures discovery against issuer has succeeded and that build has run
// exactly once, on the provider it produced.
//
// build runs while the lock is held, so a caller may populate the fields
// its other methods read without a second lock of its own: every reader
// reaches them through a Do that took the same mutex.
func (d *Discovery) Do(ctx context.Context, issuer string, build func(*oidc.Provider)) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.built {
		return nil
	}
	if d.lastErr != nil && d.now().Sub(d.lastErrAt) < d.retryAfter() {
		return d.lastErr
	}

	// Discovery is shared, so it does not run on the caller's context: the
	// first sign-in to arrive would otherwise decide, by abandoning its
	// request, whether every later one has a provider. The deadline is this
	// package's own, and the client carrying it is installed here because
	// go-oidc reaches for http.DefaultClient when nothing else is there.
	fetchCtx, cancel := context.WithTimeout(
		WithOutboundHTTPClient(context.WithoutCancel(ctx)), OutboundTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(fetchCtx, issuer)
	if err != nil {
		d.lastErr = fmt.Errorf("authn: oidc discovery for %s: %w", issuer, err)
		d.lastErrAt = d.now()
		return d.lastErr
	}
	build(provider)
	d.built = true
	d.lastErr = nil
	return nil
}

func (d *Discovery) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Discovery) retryAfter() time.Duration {
	if d.RetryAfter > 0 {
		return d.RetryAfter
	}
	return DefaultRetryAfter
}
