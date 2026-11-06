// Package brand resolves the deployment's display name (NF_APP_NAME).
// The name is a display label only; identifiers such as JWT issuers and
// key-derivation labels never use it.
package brand

import (
	"errors"
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
