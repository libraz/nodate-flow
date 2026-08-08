package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"
)

// ErrInvalidBaseURL is returned when a provider's configured base URL is
// present but not a parseable absolute http(s) URL. Validating at
// construction turns a malformed ai_providers.base_url into a fast, stable
// failure instead of an opaque transport error mid-call.
var ErrInvalidBaseURL = errors.New("ai/providers: invalid base url")

// ErrBaseURLDestinationNotAllowed is returned when a provider's base URL
// names an address the deployment must never dial: loopback, link-local,
// RFC 1918 / ULA private space, multicast, and the other non-public
// unicast ranges.
//
// ai_providers.base_url is set by a workspace admin, and every provider
// implementation POSTs to it and hands the response body back. That is a
// full request-forgery primitive into the network the api process sits in:
// port scanning by response timing, and the contents of whatever answers.
// The same admin privilege is already held to this rule when it registers
// a webhook, so the threat model applies here as well.
var ErrBaseURLDestinationNotAllowed = errors.New("ai/providers: base url destination is not allowed")

// allowPrivateEnv is the escape hatch operators set to permit private
// destinations. Local inference — Ollama on 127.0.0.1:11434, an
// OpenAI-compatible server on the LAN — is a legitimate deployment, but it
// is the operator's decision, not a workspace admin's, so it is enabled
// per deployment rather than per provider row. It must not be set in a
// deployment where workspace admins are not fully trusted.
const allowPrivateEnv = "NF_FLOW_AI_ALLOW_PRIVATE"

// allowPrivateDestinations reports whether the escape hatch is enabled.
func allowPrivateDestinations() bool {
	v := os.Getenv(allowPrivateEnv)
	return v == "1" || v == "true"
}

// isDisallowedIP reports whether ip is outside public unicast space:
// unspecified, loopback, RFC 1918 / ULA private ranges, link-local
// (unicast and multicast — this is what covers the cloud metadata service
// at 169.254.169.254), any multicast, the IPv4 broadcast address,
// carrier-grade NAT (100.64.0.0/10), and the 0.0.0.0/8 block.
//
// The rule is deliberately identical to the webhook delivery guard's. Two
// admin-configured outbound destinations judged by two different policies
// is how one of them ends up being the way around the other.
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
		if ip4.Equal(net.IPv4bcast) {
			return true
		}
		if ip4[0] == 0 {
			return true
		}
		if ip4[0] == 100 && ip4[1]&0xC0 == 64 {
			return true
		}
	}
	return false
}

// validateBaseURL is the resolution-free gate: an empty base URL means
// "use the provider's default endpoint", anything else must be an absolute
// http(s) URL with a host, no embedded credentials, and — when the host is
// a literal IP — a public unicast address.
//
// Hostnames are not resolved here. [New] has no context to resolve under,
// and a name that resolves inside the network is caught at connect time by
// [safeControl], which is also the only check a DNS record that changes
// between validation and the call cannot slip past.
func validateBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme must be http or https", ErrInvalidBaseURL)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidBaseURL)
	}
	if u.User != nil {
		return fmt.Errorf("%w: userinfo is not allowed", ErrInvalidBaseURL)
	}
	if allowPrivateDestinations() {
		return nil
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isDisallowedIP(ip) {
		return fmt.Errorf("%w: %s", ErrBaseURLDestinationNotAllowed, ip)
	}
	return nil
}

// ValidateBaseURLDestination is the submit-time gate for a base URL an
// admin has just typed. It is [validateBaseURL] plus name resolution, so a
// hostname pointing inside the network is refused at the point it is
// entered — with a field to blame — rather than at the first completion,
// where the only visible result is that the workspace's AI stopped.
//
// It is not the security boundary: a name that resolves publicly now can
// resolve privately later. [safeControl] is what enforces the policy on
// the address actually connected to.
func ValidateBaseURLDestination(ctx context.Context, raw string) error {
	if err := validateBaseURL(raw); err != nil {
		return err
	}
	if raw == "" || allowPrivateDestinations() {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBaseURL, err)
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		// A literal address was already judged by validateBaseURL.
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("%w: %s does not resolve", ErrBaseURLDestinationNotAllowed, host)
	}
	for _, a := range addrs {
		if isDisallowedIP(a.IP) {
			return fmt.Errorf("%w: %s resolves to %s", ErrBaseURLDestinationNotAllowed, host, a.IP)
		}
	}
	return nil
}

// safeControl is the net.Dialer Control hook on the shared provider HTTP
// client. It runs after DNS resolution, on the address the socket is about
// to connect to, which makes it the authoritative check: the base URL that
// passed validation and the address finally dialed are not required to
// agree, and only this sees the latter.
func safeControl(network, address string, _ syscall.RawConn) error {
	switch network {
	case "tcp4", "tcp6":
	default:
		return fmt.Errorf("%w: network %s", ErrBaseURLDestinationNotAllowed, network)
	}
	if allowPrivateDestinations() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBaseURLDestinationNotAllowed, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || isDisallowedIP(ip) {
		return fmt.Errorf("%w: connect to %s", ErrBaseURLDestinationNotAllowed, host)
	}
	return nil
}
