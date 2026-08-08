package integrations

import (
	"errors"
	"fmt"
	"io"
)

// maxProviderResponseBytes bounds how much of an OAuth provider's HTTP
// response this package will hold in memory.
//
// Every provider response read here is a small JSON envelope: a token
// grant, a revocation acknowledgement, or a user profile. None of them
// approach a megabyte. The bound exists because the size is not ours to
// decide — the process on the other end of the socket picks it, and a
// compromised, misconfigured, or merely broken endpoint answering a
// token exchange with an endless stream would otherwise be read into
// the auth-api heap in full.
const maxProviderResponseBytes = 1 << 20 // 1 MiB

// maxProviderErrorPreviewBytes bounds how much of a non-2xx body may
// travel inside an error string. Errors reach logs, and a log line is
// not a place to reproduce a megabyte of upstream HTML.
const maxProviderErrorPreviewBytes = 1 << 10 // 1 KiB

// ErrResponseTooLarge reports a provider response that exceeded
// maxProviderResponseBytes. It is returned instead of the parsed result
// because a truncated JSON envelope is not a partial answer — it is an
// answer we cannot read, and treating it as one would surface as a
// confusing unmarshal failure at the call site.
var ErrResponseTooLarge = errors.New("integrations: provider response exceeds the size limit")

// readProviderBody reads a provider response body up to
// maxProviderResponseBytes.
//
// One extra byte is requested so that hitting the ceiling is
// distinguishable from a body that merely ends there; when it is
// reached the truncated bytes are returned alongside ErrResponseTooLarge
// so a caller that only wants a diagnostic preview still has something
// to show.
func readProviderBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxProviderResponseBytes+1))
	if err != nil {
		return body, err
	}
	if len(body) > maxProviderResponseBytes {
		return body[:maxProviderResponseBytes], ErrResponseTooLarge
	}
	return body, nil
}

// errorPreview renders a response body for inclusion in an error
// message, truncating it to maxProviderErrorPreviewBytes.
func errorPreview(body []byte) string {
	if len(body) <= maxProviderErrorPreviewBytes {
		return string(body)
	}
	return fmt.Sprintf("%s... (%d bytes truncated)",
		body[:maxProviderErrorPreviewBytes], len(body)-maxProviderErrorPreviewBytes)
}
