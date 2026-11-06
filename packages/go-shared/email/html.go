package email

import (
	"html"
	"strings"

	"github.com/libraz/nodate-flow/packages/go-shared/brand"
)

// renderHTML builds the HTML alternative of a plain-text body, with the
// brand header on top. Tokenization runs on the raw text, before escaping.
func renderHTML(b Brand, body string) string {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html><body style=\"font-family:sans-serif;line-height:1.5\">\n")
	sb.WriteString(brandHeader(b))
	text := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	for _, para := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(para) == "" {
			continue
		}
		sb.WriteString("<p>")
		sb.WriteString(strings.ReplaceAll(linkify(para), "\n", "<br>"))
		sb.WriteString("</p>\n")
	}
	sb.WriteString("</body></html>\n")
	return sb.String()
}

func brandHeader(b Brand) string {
	name := html.EscapeString(b.Name)
	switch {
	case isHTTPS(b.LogoURL):
		return `<p><img src="` + html.EscapeString(b.LogoURL) + `" alt="` + name +
			`" style="height:40px;max-width:240px"></p>` + "\n"
	case isHTTPS(b.IconURL):
		return `<p><img src="` + html.EscapeString(b.IconURL) + `" alt="" width="32" height="32" ` +
			`style="vertical-align:middle"> <strong>` + name + "</strong></p>\n"
	default:
		return "<p><strong>" + name + "</strong></p>\n"
	}
}

// isHTTPS reports whether u is an https image URL; data: URIs and blanks
// count as absent.
func isHTTPS(u string) bool {
	return len(u) >= 8 && strings.EqualFold(u[:8], "https://") && !brand.IsDataURI(u)
}

// linkify escapes s, turning http(s) URL runs into anchors.
func linkify(s string) string {
	var sb strings.Builder
	for len(s) > 0 {
		i := nextURLStart(s)
		if i < 0 {
			sb.WriteString(html.EscapeString(s))
			break
		}
		sb.WriteString(html.EscapeString(s[:i]))
		rest := s[i:]
		end := strings.IndexAny(rest, " \t\r\n<>\"")
		if end < 0 {
			end = len(rest)
		}
		u := strings.TrimRight(rest[:end], ".,;:!?)")
		if u == "https://" || u == "http://" {
			// A bare scheme is not a link.
			sb.WriteString(html.EscapeString(rest[:end]))
			s = rest[end:]
			continue
		}
		esc := html.EscapeString(u)
		sb.WriteString(`<a href="` + esc + `">` + esc + `</a>`)
		s = rest[len(u):]
	}
	return sb.String()
}

func nextURLStart(s string) int {
	best := -1
	for _, p := range []string{"https://", "http://"} {
		if i := strings.Index(s, p); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	return best
}
