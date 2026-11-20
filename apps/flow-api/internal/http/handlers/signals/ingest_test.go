package signals

import "testing"

func TestExtractTaskMarker(t *testing.T) {
	t.Parallel()

	const (
		first  = "01931c2c-7e3a-7c66-9b85-3a0c9b3a8eaa"
		second = "01931c2c-7e3a-7c66-9b85-3a0c9b3a8ebb"
	)
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{"no marker", `{"text":"hello"}`, "", false},
		{"one marker", `{"text":"done tnk:` + first + ` ok"}`, first, true},
		{"marker at end of body", "tnk:" + first, first, true},
		{"malformed uuid", "tnk:not-a-uuid-at-all-not-a-uuid-at-all!", "", false},
		{"too short", "tnk:" + first[:20], "", false},
		{"multiple markers use the first", "tnk:" + first + " tnk:" + second, first, true},
		{"malformed first is not skipped", "tnk:zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz tnk:" + second, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := extractTaskMarker([]byte(tc.body))
			if got != tc.want || ok != tc.ok {
				t.Fatalf("extractTaskMarker(%q) = (%q, %v), want (%q, %v)", tc.body, got, ok, tc.want, tc.ok)
			}
		})
	}
}
