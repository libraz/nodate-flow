package mentions

import (
	"fmt"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// Two canonical UUID v7 values standing in for workspace members. Only
// their text matters here: [Extract] never touches a database.
const (
	idAnn = "019649b0-0000-7000-8000-000000000001"
	idBee = "019649b0-0000-7000-8000-000000000002"
)

// mention renders the notation the way a body carries it.
func mention(name, id string) string {
	return fmt.Sprintf("@[%s](user:%s)", name, id)
}

// stringsOf renders the result in the form the table's want column uses.
func stringsOf(ids []types.PublicID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

func TestExtract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "empty body names nobody",
			body: "",
		},
		{
			name: "prose with no notation names nobody",
			body: "ship it on Friday",
		},
		{
			name: "a bare at-name is not a mention",
			body: "@ann please review",
		},
		{
			name: "a link with no user prefix is not a mention",
			body: "@[Ann](https://example.test/ann)",
		},
		{
			name: "one mention",
			body: "please review " + mention("Ann", idAnn),
			want: []string{idAnn},
		},
		{
			name: "several mentions keep first-appearance order",
			body: mention("Bee", idBee) + " and " + mention("Ann", idAnn),
			want: []string{idBee, idAnn},
		},
		{
			name: "the same person named twice is one mention",
			body: mention("Ann", idAnn) + " ... " + mention("Ann A.", idAnn),
			want: []string{idAnn},
		},
		{
			name: "the id decides, not the case it was typed in",
			body: mention("Ann", idAnn) + mention("Ann", strings.ToUpper(idAnn)),
			want: []string{idAnn},
		},
		{
			name: "an empty display name still resolves",
			body: mention("", idAnn),
			want: []string{idAnn},
		},
		{
			name: "brackets inside the display name are part of it",
			body: mention("Ann [on leave]", idAnn),
			want: []string{idAnn},
		},
		{
			name: "a closing parenthesis inside the display name is part of it",
			body: mention("Ann (she/her)", idAnn),
			want: []string{idAnn},
		},
		{
			name: "an unterminated notation names nobody",
			body: "@[Ann](user:" + idAnn,
			want: nil,
		},
		{
			name: "an opening marker that never reaches the id marker names nobody",
			body: "@[Ann] please review",
			want: nil,
		},
		{
			name: "an id that is not a UUID names nobody",
			body: mention("Ann", "ann"),
			want: nil,
		},
		{
			name: "an empty id names nobody",
			body: mention("Ann", ""),
			want: nil,
		},
		{
			name: "a malformed notation does not hide the one after it",
			body: mention("Ann", "ann") + " " + mention("Bee", idBee),
			want: []string{idBee},
		},
		{
			name: "an abandoned opening marker does not swallow the mention typed after it",
			body: "@[oops " + mention("Bee", idBee),
			want: []string{idBee},
		},
		{
			name: "an unterminated notation before a complete one",
			body: "@[Ann](user:" + idAnn + " then " + mention("Bee", idBee),
			want: []string{idBee},
		},
		{
			// Markdown structure is not interpreted; see Extract.
			name: "a notation inside a fenced block is still a mention",
			body: "```\n" + mention("Ann", idAnn) + "\n```",
			want: []string{idAnn},
		},
		{
			// Same limitation, inline.
			name: "a notation inside an inline code span is still a mention",
			body: "write `" + mention("Ann", idAnn) + "` to name someone",
			want: []string{idAnn},
		},
		{
			name: "mentions on either side of other markdown",
			body: mention("Ann", idAnn) + " see [the spec](https://example.test/spec) " + mention("Bee", idBee),
			want: []string{idAnn, idBee},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stringsOf(Extract(tc.body))
			if len(got) != len(tc.want) {
				t.Fatalf("Extract(%q) = %v, want %v", tc.body, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Extract(%q) = %v, want %v", tc.body, got, tc.want)
				}
			}
		})
	}
}

// TestExtractTerminates guards the scanner against a body that could put
// it in a loop: every branch has to advance past the marker it rejected.
func TestExtractTerminates(t *testing.T) {
	t.Parallel()

	bodies := []string{
		strings.Repeat("@[", 512),
		strings.Repeat("@[](user:)", 512),
		strings.Repeat("@[@[](user:", 512),
		strings.Repeat("](user:", 512),
		strings.Repeat("@[a](user:b)", 512),
	}
	for _, body := range bodies {
		if got := Extract(body); len(got) != 0 {
			t.Errorf("Extract(%.20q...) = %v, want no mentions", body, stringsOf(got))
		}
	}
}
