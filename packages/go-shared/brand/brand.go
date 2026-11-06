// Package brand resolves the deployment's display name (NF_APP_NAME) and
// logo/icon image URLs.
// The name is a display label only; identifiers such as JWT issuers and
// key-derivation labels never use it.
package brand

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DefaultAppName is the display name used when NF_APP_NAME is unset or blank.
const DefaultAppName = "nodate-flow"

// MaxAppNameRunes is the longest accepted name, counted in code points.
const MaxAppNameRunes = 64

// ResolveAppName trims raw and validates it, returning DefaultAppName when
// the trimmed value is empty. Errors name NF_APP_NAME and the broken rule,
// never the value.
func ResolveAppName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return DefaultAppName, nil
	}
	if utf8.RuneCountInString(name) > MaxAppNameRunes {
		return "", errors.New("NF_APP_NAME must be at most 64 characters")
	}
	for _, r := range name {
		if r == ':' {
			return "", errors.New("NF_APP_NAME must not contain ':'")
		}
		if isForbiddenControl(r) {
			return "", errors.New("NF_APP_NAME must not contain control or bidirectional control characters")
		}
	}
	return name, nil
}

// NameOrDefault returns DefaultAppName when s is empty. It is for callers
// that do not pass through config loading.
func NameOrDefault(s string) string {
	if s == "" {
		return DefaultAppName
	}
	return s
}

func isForbiddenControl(r rune) bool {
	switch {
	case r <= 0x1F, r >= 0x7F && r <= 0x9F:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// MaxDataURILength is the longest accepted data: image URI, in characters.
const MaxDataURILength = 32768

const imageSpaceChars = " \t\n\r\f\v"

var dataImagePrefix = regexp.MustCompile(`(?i)^data:image/(png|svg\+xml|webp|jpeg|gif)[;,]`)

// ResolveImageURL trims raw and validates it as an https URL or a bounded
// data:image URI, returning "" when the trimmed value is empty. Errors name
// envName and the broken rule, never the value.
func ResolveImageURL(envName, raw string) (string, error) {
	v := strings.Trim(raw, imageSpaceChars)
	if v == "" {
		return "", nil
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x21 || v[i] > 0x7e {
			return "", fmt.Errorf("%s must contain only printable ASCII characters", envName)
		}
	}
	badKind := fmt.Errorf("%s must be an https URL or a data:image/(png|svg+xml|webp|jpeg|gif) URI", envName)
	if IsDataURI(v) {
		if !dataImagePrefix.MatchString(v) {
			return "", badKind
		}
		if len(v) > MaxDataURILength {
			return "", fmt.Errorf("%s data URI must be at most %d characters", envName, MaxDataURILength)
		}
		return v, nil
	}
	u, err := url.Parse(v)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return "", badKind
	}
	return v, nil
}

// IsDataURI reports whether s starts with "data:", ignoring case.
func IsDataURI(s string) bool {
	return len(s) >= 5 && strings.EqualFold(s[:5], "data:")
}
