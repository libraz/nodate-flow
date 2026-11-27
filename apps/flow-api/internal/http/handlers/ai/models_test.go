package ai

import (
	"strings"
	"testing"
)

func TestValidModelName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		in    string
		want  string
		valid bool
	}{
		{"empty", "", "", false},
		{"whitespace only", " \t\n ", "", false},
		{"surrounding whitespace trimmed", "  gpt-4o \t", "gpt-4o", true},
		{"inner space", "gpt 4o", "", false},
		{"control char", "gpt\x014o", "", false},
		{"inner tab", "gpt\t4o", "", false},
		{"DEL", "gpt\x7f4o", "", false},
		{"non-ASCII", "モデル", "", false},
		{"non-ASCII mixed", "gpt-4o-é", "", false},
		{"1 byte", "a", "a", true},
		{"128 bytes", strings.Repeat("a", 128), strings.Repeat("a", 128), true},
		{"129 bytes", strings.Repeat("a", 129), "", false},
		{"128 bytes plus whitespace, refused earlier by the schema bound", " " + strings.Repeat("a", 128) + " ", strings.Repeat("a", 128), true},
		{"ollama tag", "llama3.2:8b", "llama3.2:8b", true},
		{"slash namespace", "anthropic/claude-x", "anthropic/claude-x", true},
		{"mixed case namespace", "Qwen/Qwen2.5-7B", "Qwen/Qwen2.5-7B", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := validModelName(tc.in)
			if ok != tc.valid || got != tc.want {
				t.Fatalf("validModelName(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.valid)
			}
		})
	}
}
