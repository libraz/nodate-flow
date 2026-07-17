package webhook

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"
)

// ErrURLDisallowed is the sentinel returned by the SSRF guard when a
// webhook URL (or an address it resolves to) must not be contacted.
// Handlers map it to WEBHOOK.SUBSCRIPTION.URL_INVALID; the delivery
// worker records it as a permanent failure reason.
var ErrURLDisallowed = errors.New("webhook: URL destination is not allowed")

// maxRedirects caps the redirect chain followed during delivery.
const maxRedirects = 5

// dialTimeout bounds the TCP connect phase of a delivery attempt.
const dialTimeout = 5 * time.Second

// allowPrivateDestinations reports whether the NF_WEBHOOK_ALLOW_PRIVATE
// escape hatch is enabled. It relaxes the https-only and public-address
// requirements so local development and the e2e suite can deliver to
// loopback httptest targets. It must never be set in production; the
// default (unset) keeps the full SSRF policy active.
func allowPrivateDestinations() bool {
	v := os.Getenv("NF_WEBHOOK_ALLOW_PRIVATE")
	return v == "1" || v == "true"
}

// isDisallowedIP reports whether ip must never be a webhook delivery
// target. It rejects everything that is not a public unicast address:
// unspecified, loopback, RFC 1918 / ULA private ranges, link-local
// (unicast and multicast), any multicast, the IPv4 broadcast address,
// carrier-grade NAT (100.64.0.0/10), and the 0.0.0.0/8 block.
func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// IPv4 broadcast.
		if ip4.Equal(net.IPv4bcast) {
			return true
		}
		// "This network" (0.0.0.0/8).
		if ip4[0] == 0 {
			return true
		}
		// Carrier-grade NAT (100.64.0.0/10).
		if ip4[0] == 100 && ip4[1]&0xC0 == 64 {
			return true
		}
	}
	return false
}

// validateURLSyntax performs the resolution-free part of webhook URL
// validation: parseability, https scheme, no embedded credentials, and
// a non-empty host. It returns the parsed URL on success.
func validateURLSyntax(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrURLDisallowed, err)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowPrivateDestinations():
	default:
		return nil, fmt.Errorf("%w: scheme must be https", ErrURLDisallowed)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: userinfo is not allowed", ErrURLDisallowed)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: host is required", ErrURLDisallowed)
	}
	return u, nil
}

// ValidateURL checks that raw is an acceptable webhook destination:
// a well-formed https URL without embedded credentials whose host
// resolves exclusively to public unicast addresses. Every resolved
// address is checked; a single disallowed address rejects the URL.
//
// This is the create-time / test-time gate. It is intentionally paired
// with the dial-time guard in [NewSafeClient], which re-checks the
// actual connect address so a DNS record that changes between
// validation and delivery (DNS rebinding) cannot bypass the policy.
func ValidateURL(ctx context.Context, raw string) error {
	u, err := validateURLSyntax(raw)
	if err != nil {
		return err
	}
	if allowPrivateDestinations() {
		return nil
	}
	host := u.Hostname()

	// Literal IP: no resolution needed.
	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedIP(ip) {
			return fmt.Errorf("%w: %s", ErrURLDisallowed, ip)
		}
		return nil
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("%w: host does not resolve", ErrURLDisallowed)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: host does not resolve", ErrURLDisallowed)
	}
	for _, a := range addrs {
		if isDisallowedIP(a.IP) {
			return fmt.Errorf("%w: %s resolves to %s", ErrURLDisallowed, host, a.IP)
		}
	}
	return nil
}

// safeControl is the net.Dialer Control hook that vets the address the
// socket is actually about to connect to. Because it runs after DNS
// resolution, it is the authoritative defense against DNS rebinding:
// whatever the hostname resolved to at connect time is what gets
// checked here.
func safeControl(network, address string, _ syscall.RawConn) error {
	switch network {
	case "tcp4", "tcp6":
	default:
		return fmt.Errorf("%w: network %s", ErrURLDisallowed, network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrURLDisallowed, err)
	}
	if allowPrivateDestinations() {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || isDisallowedIP(ip) {
		return fmt.Errorf("%w: connect to %s", ErrURLDisallowed, host)
	}
	return nil
}

// NewSafeClient builds the HTTP client used for webhook delivery. It
// layers three SSRF defenses on top of the create-time validation:
//
//   - a dialer Control hook that rejects connects to non-public
//     addresses (defeats DNS rebinding),
//   - a CheckRedirect that re-validates every redirect target with
//     [ValidateURL] and caps the chain length, and
//   - no proxy, so NF_/HTTP_PROXY environment cannot reroute delivery
//     around the connect-time check.
func NewSafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: dialTimeout,
		Control: safeControl,
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w: too many redirects", ErrURLDisallowed)
			}
			return ValidateURL(req.Context(), req.URL.String())
		},
	}
}
