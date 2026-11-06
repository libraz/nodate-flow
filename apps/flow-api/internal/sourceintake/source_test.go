package sourceintake

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectProviderPayloads(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		kind    string
		payload string
		want    Detail
	}{
		{
			name:   "github issue",
			source: "github",
			kind:   "issues.opened",
			payload: `{"issue":{"title":"Fix the import","body":"The import drops accented names.",` +
				`"html_url":"https://github.com/acme/app/issues/7"}}`,
			want: Detail{
				Title:     "Fix the import",
				Body:      "The import drops accented names.",
				SourceURL: "https://github.com/acme/app/issues/7",
			},
		},
		{
			name:   "slack message",
			source: "slack",
			kind:   "message",
			payload: `{"event":{"type":"message","text":"Review the rollout plan",` +
				`"permalink":"https://acme.slack.com/archives/C123/p456"}}`,
			want: Detail{
				Title:     "Review the rollout plan",
				Body:      "Review the rollout plan",
				SourceURL: "https://acme.slack.com/archives/C123/p456",
			},
		},
		{
			name:   "manual capture",
			source: "manual",
			kind:   "manual",
			payload: `{"title":"Renew the certificate","body":"Expires next month.",` +
				`"url":"https://status.example.test/certificates/4"}`,
			want: Detail{
				Title:     "Renew the certificate",
				Body:      "Expires next month.",
				SourceURL: "https://status.example.test/certificates/4",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Project(tt.source, tt.kind, []byte(tt.payload))
			if got != tt.want {
				t.Fatalf("Project() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestProjectDropsUnsafeURLAndBoundsText(t *testing.T) {
	payload, err := json.Marshal(map[string]string{
		"title": strings.Repeat("t", maxTitleRunes+10),
		"body":  strings.Repeat("b", maxBodyRunes+10),
		"url":   "javascript:alert(document.cookie)",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := Project("manual", "manual", payload)
	if len([]rune(got.Title)) != maxTitleRunes {
		t.Fatalf("title runes = %d, want %d", len([]rune(got.Title)), maxTitleRunes)
	}
	if len([]rune(got.Body)) != maxBodyRunes {
		t.Fatalf("body runes = %d, want %d", len([]rune(got.Body)), maxBodyRunes)
	}
	if got.SourceURL != "" {
		t.Fatalf("unsafe URL = %q, want empty", got.SourceURL)
	}
}

func TestProjectFieldsBoundsCapturedSourceDetails(t *testing.T) {
	got := ProjectFields(
		" GITHUB ", "issues.opened", "Title\x00",
		strings.Repeat("body", maxBodyRunes), "javascript:alert(1)",
	)
	if got.Title != "Title" {
		t.Fatalf("title = %q, want cleaned title", got.Title)
	}
	if len([]rune(got.Body)) != maxBodyRunes {
		t.Fatalf("body runes = %d, want %d", len([]rune(got.Body)), maxBodyRunes)
	}
	if got.SourceURL != "" {
		t.Fatalf("unsafe URL = %q, want empty", got.SourceURL)
	}
}

func TestSafeHTTPURLRejectsRelativeAndCredentialURLs(t *testing.T) {
	for _, raw := range []string{"/relative/path", "javascript:alert(1)", "https://user:pass@example.test/a"} {
		if got := safeHTTPURL(raw); got != "" {
			t.Errorf("safeHTTPURL(%q) = %q, want empty", raw, got)
		}
	}
}

func TestSafeHTTPURLRejectsNonHTTPAndHostlessForms(t *testing.T) {
	for _, raw := range []string{
		"JavaScript:alert(1)",
		"JAVASCRIPT:alert(1)",
		"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
		"DATA:text/html,<script>alert(1)</script>",
		"//example.test/protocol-relative",
		"https:example.test/no-authority",
		"https:/example.test/one-slash",
	} {
		if got := safeHTTPURL(raw); got != "" {
			t.Errorf("safeHTTPURL(%q) = %q, want empty", raw, got)
		}
	}
}

func TestSafeHTTPURLAcceptsMixedCaseHTTPSchemes(t *testing.T) {
	for raw, want := range map[string]string{
		"HTTPS://example.test/a": "https://example.test/a",
		"Http://example.test/b":  "http://example.test/b",
	} {
		if got := safeHTTPURL(raw); got != want {
			t.Errorf("safeHTTPURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
