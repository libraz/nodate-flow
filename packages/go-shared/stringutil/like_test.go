package stringutil

import "testing"

func TestEscapeLike(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty string", in: "", want: ""},
		{name: "no metacharacters", in: "hello world", want: "hello world"},
		{name: "percent only", in: "50%", want: `50\%`},
		{name: "underscore only", in: "a_b", want: `a\_b`},
		{name: "backslash only", in: `a\b`, want: `a\\b`},
		{name: "all metacharacters", in: `a%b_c\d`, want: `a\%b\_c\\d`},
		{name: "backslash escaped first", in: `\%`, want: `\\\%`},
		{name: "consecutive metacharacters", in: `%%__\\`, want: `\%\%\_\_\\\\`},
		{name: "unicode passthrough", in: "日本語 café", want: "日本語 café"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EscapeLike(tc.in)
			if got != tc.want {
				t.Fatalf("EscapeLike(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
