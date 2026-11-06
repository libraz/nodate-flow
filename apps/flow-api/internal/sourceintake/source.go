// Package sourceintake projects authenticated signal payloads into bounded,
// plain-text fields suitable for review in Inbox and Intake. It never treats
// provider JSON as HTML and only returns links that are safe to use as HTTP(S)
// navigation targets.
package sourceintake

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode"
)

const (
	maxTitleRunes = 500
	maxBodyRunes  = 50_000
	maxURLBytes   = 2_048
)

// Detail is the source-specific, displayable projection of a signal. Title
// and Body are plain text. SourceURL is empty when the provider payload does
// not contain a valid absolute HTTP(S) URL.
type Detail struct {
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	SourceURL string `json:"sourceUrl,omitempty"`
}

// Project extracts only known title/body/link fields for the supported
// source families. Unknown sources get a conservative generic title and no
// body or link. The stored payload remains the source of truth and is never
// interpolated into markup.
func Project(source, kind string, payload []byte) Detail {
	var root map[string]any
	if len(payload) == 0 || json.Unmarshal(payload, &root) != nil {
		root = nil
	}

	source = strings.ToLower(strings.TrimSpace(source))
	kind = cleanText(kind, maxTitleRunes)
	var title, body, rawURL string
	switch source {
	case "github":
		issue := objectAt(root, "issue")
		pull := objectAt(root, "pull_request")
		comment := objectAt(root, "comment")
		title = firstString(
			stringAt(issue, "title"), stringAt(pull, "title"), stringAt(root, "title"),
		)
		body = firstString(
			stringAt(issue, "body"), stringAt(pull, "body"), stringAt(comment, "body"), stringAt(root, "body"),
		)
		rawURL = firstString(
			stringAt(issue, "html_url"), stringAt(pull, "html_url"), stringAt(comment, "html_url"),
			stringAt(root, "html_url"), stringAt(root, "source_url"), stringAt(root, "sourceUrl"),
		)
	case "slack":
		event := objectAt(root, "event")
		message := objectAt(event, "message")
		if len(message) == 0 {
			message = objectAt(root, "message")
		}
		body = firstString(
			stringAt(event, "text"), stringAt(message, "text"), stringAt(root, "text"),
		)
		title = firstString(
			stringAt(root, "title"), stringAt(event, "title"),
			firstLine(body),
		)
		rawURL = firstString(
			stringAt(event, "permalink"), stringAt(message, "permalink"), stringAt(root, "permalink"),
			stringAt(root, "source_url"), stringAt(root, "sourceUrl"), stringAt(root, "url"),
		)
	case "manual":
		title = stringAt(root, "title")
		body = stringAt(root, "body")
		rawURL = firstString(stringAt(root, "sourceUrl"), stringAt(root, "source_url"), stringAt(root, "url"))
	}

	return ProjectFields(source, kind, title, body, rawURL)
}

// ProjectFields applies the source display bounds and safe-link policy to
// fields already captured in intake storage. This lets task backlink reads
// return the original source detail without loading or exposing raw payloads.
func ProjectFields(source, kind, title, body, rawURL string) Detail {
	source = strings.ToLower(strings.TrimSpace(source))
	kind = cleanText(kind, maxTitleRunes)
	title = cleanText(title, maxTitleRunes)
	if title == "" {
		title = kind
	}
	if title == "" {
		if source != "" {
			title = source + " signal"
		} else {
			title = "Incoming signal"
		}
	}
	return Detail{
		Title:     title,
		Body:      cleanText(body, maxBodyRunes),
		SourceURL: safeHTTPURL(rawURL),
	}
}

func objectAt(object map[string]any, key string) map[string]any {
	child, _ := object[key].(map[string]any)
	return child
}

func stringAt(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstLine(value string) string {
	if at := strings.IndexAny(value, "\r\n"); at >= 0 {
		return value[:at]
	}
	return value
}

func cleanText(value string, maxRunes int) string {
	value = strings.ToValidUTF8(value, "�")
	var builder strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(value) {
		if r == 0 || (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') {
			continue
		}
		if count == maxRunes {
			break
		}
		builder.WriteRune(r)
		count++
	}
	return strings.TrimSpace(builder.String())
}

func safeHTTPURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxURLBytes {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '\\' {
			return ""
		}
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}
