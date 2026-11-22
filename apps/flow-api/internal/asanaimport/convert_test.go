package asanaimport

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

var convUsers = []asana.User{
	{GID: "100", Name: "Ops", Email: "ops@example.com"},
	{GID: "101", Name: "Hanako", Email: "hanako@example.com"},
	{GID: "102", Name: "Jiro"},
	{GID: "103", Name: "Taro", Email: "taro@example.com"},
	{GID: "104", Name: "Saburo", Email: "saburo@example.com"},
}

// testConv converts for project 900, whose members are ops and hanako;
// saburo is an account outside it and taro has none.
func testConv(t *testing.T, sourceUsers map[string]string) *projectConv {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	accounts := map[string]bool{"ops@example.com": true, "hanako@example.com": true, "saburo@example.com": true, "jiro@example.com": true}
	return &projectConv{
		loc:        loc,
		people:     newPeople(convUsers, sourceUsers, func(e string) bool { return accounts[e] }),
		members:    map[string]bool{"ops@example.com": true, "hanako@example.com": true},
		projectGID: "900",
		sections:   map[string]string{"Untitled section": "", "Doing": "In progress"},
	}
}

func day(s string) time.Time {
	d, _ := time.Parse(dateLayout, s)
	return d
}

func TestConvert(t *testing.T) {
	// Pairwise over title × due × start × completed × assignee, with
	// "start after due" excluded when there is no due date.
	rows := []struct {
		title, due, start string
		completed         bool
		assignee          string
	}{
		{"empty", "both", "after", true, "notmember"},
		{"short", "both", "before", false, "unmatched"},
		{"over500", "on", "none", true, "resolved"},
		{"at500", "both", "after", false, "resolved"},
		{"empty", "on", "none", false, "none"},
		{"over500", "at", "after", true, "unmatched"},
		{"short", "on", "after", true, "none"},
		{"at500", "none", "before", true, "none"},
		{"over500", "both", "none", true, "none"},
		{"at500", "on", "before", false, "notmember"},
		{"empty", "at", "before", false, "none"},
		{"over500", "none", "before", false, "resolved"},
		{"short", "none", "none", true, "unmatched"},
		{"over500", "at", "before", false, "notmember"},
		{"over500", "none", "before", true, "notmember"},
		{"at500", "both", "none", false, "unmatched"},
		{"empty", "on", "none", true, "unmatched"},
		{"short", "on", "after", true, "resolved"},
		{"at500", "at", "none", true, "resolved"},
		{"short", "both", "none", false, "notmember"},
		{"empty", "none", "before", true, "notmember"},
		{"short", "at", "after", false, "none"},
		{"empty", "at", "after", false, "resolved"},
	}
	titles := map[string]string{
		"empty": "  ", "short": " Brief ", "at500": strings.Repeat("あ", 500), "over500": strings.Repeat("あ", 501),
	}
	// 16:00Z on the 9th is the 10th in Tokyo.
	dueAt := time.Date(2026, 3, 9, 16, 0, 0, 0, time.UTC)
	dueWant := map[string]string{"none": "", "on": "2026-03-10", "at": "2026-03-10", "both": "2026-03-11"}
	assignees := map[string]*asana.User{
		"resolved": &convUsers[1], "notmember": &convUsers[4], "unmatched": &convUsers[2], "none": nil,
	}

	for _, r := range rows {
		name := strings.Join([]string{r.title, r.due, r.start, r.assignee}, "/")
		t.Run(name, func(t *testing.T) {
			task := asana.Task{
				GID: "1", Name: titles[r.title], HTMLNotes: "<body>Hi</body>", Completed: r.completed,
				Assignee:    assignees[r.assignee],
				Followers:   []asana.User{convUsers[1], convUsers[0]},
				Tags:        []asana.Tag{{GID: "8", Name: "Bug"}, {GID: "9", Name: "bug"}},
				Memberships: []asana.Membership{{Project: asana.Ref{GID: "900"}, Section: &asana.Ref{GID: "S2", Name: "Untitled section"}}},
			}
			switch r.due {
			case "on":
				task.DueOn = "2026-03-10"
			case "at":
				task.DueAt = &dueAt
			case "both":
				task.DueOn, task.DueAt = "2026-03-11", &dueAt
			}
			switch r.start {
			case "before":
				task.StartOn = "2026-03-01"
			case "after":
				task.StartOn = "2026-03-20"
			}

			v, skip := testConv(t, nil).task(&asana.TaskDetail{Task: task})
			if r.title == "empty" {
				assert.Equal(t, SkipEmptyTitle, skip)
				return
			}
			require.Empty(t, skip)

			switch r.title {
			case "short":
				assert.Equal(t, "Brief", v.Title)
				assert.Equal(t, "Hi", v.Description)
			case "at500":
				assert.Equal(t, titles["at500"], v.Title)
				assert.Equal(t, "Hi", v.Description)
			case "over500":
				assert.Equal(t, strings.Repeat("あ", 499)+"…", v.Title)
				assert.Equal(t, "**"+titles["over500"]+"**\n\nHi", v.Description)
			}

			if want := dueWant[r.due]; want == "" {
				assert.False(t, v.DueOn.Valid)
			} else {
				assert.Equal(t, day(want), v.DueOn.Time)
			}
			switch r.start {
			case "none":
				assert.False(t, v.StartOn.Valid)
				assert.False(t, v.StartDropped)
			case "before":
				assert.Equal(t, day("2026-03-01"), v.StartOn.Time)
			case "after":
				assert.False(t, v.StartOn.Valid)
				assert.True(t, v.StartDropped)
			}

			assert.Equal(t, r.completed, v.Completed)
			if r.assignee == "resolved" {
				assert.Equal(t, "hanako@example.com", v.Assignee)
				assert.Equal(t, []string{"ops@example.com"}, v.Watchers)
			} else {
				assert.Empty(t, v.Assignee)
				assert.Equal(t, []string{"hanako@example.com", "ops@example.com"}, v.Watchers)
			}
			assert.Equal(t, []LabelRef{{Project: -1, Name: "Bug"}}, v.Labels)
		})
	}

	t.Run("description length", func(t *testing.T) {
		for _, c := range []struct {
			runes     int
			want      string
			truncated bool
		}{
			{50000, strings.Repeat("a", 50000), false},
			{50001, strings.Repeat("a", 49999) + "…", true},
		} {
			d := &asana.TaskDetail{Task: asana.Task{Name: "T", HTMLNotes: "<body>" + strings.Repeat("a", c.runes) + "</body>"}}
			v, _ := testConv(t, nil).task(d)
			assert.Equal(t, c.want, v.Description)
			assert.Equal(t, c.truncated, v.Truncated)
		}
	})

	t.Run("external links close the description in gid order", func(t *testing.T) {
		d := &asana.TaskDetail{
			Task: asana.Task{Name: "T", HTMLNotes: "<body>Hi</body>"},
			Attachments: []asana.Attachment{
				{GID: "20", Name: "b", Host: "gdrive", ViewURL: "u20"},
				{GID: "3", Name: "a", Host: "dropbox", ViewURL: "u3"},
				{GID: "4", Name: "c", Host: "asana", ViewURL: "u4"},
				{GID: "5", Name: "d.png", Host: "asana", DownloadURL: "https://x", ViewURL: "u5"},
			},
		}
		v, _ := testConv(t, nil).task(d)
		assert.Equal(t, "Hi\n\n---\n- [a](u3)\n- [c](u4)\n- [b](u20)", v.Description)
	})

	t.Run("section labels", func(t *testing.T) {
		long := strings.Repeat("x", 65)
		for _, c := range []struct {
			project, section string
			want             []LabelRef
		}{
			{"900", "Untitled section", nil},
			{"900", "Doing", []LabelRef{{Project: 0, Name: "In progress"}}},
			{"900", "Todo", []LabelRef{{Project: 0, Name: "Todo"}}},
			{"900", long, []LabelRef{{Project: 0, Name: long[:64]}}},
			{"901", "Todo", nil},
		} {
			d := &asana.TaskDetail{Task: asana.Task{Name: "T", Memberships: []asana.Membership{
				{Project: asana.Ref{GID: c.project}, Section: &asana.Ref{GID: "S", Name: c.section}},
			}}}
			v, _ := testConv(t, nil).task(d)
			assert.Equal(t, c.want, v.Labels, c.section)
		}
	})

	t.Run("attachments", func(t *testing.T) {
		long := strings.Repeat("n", 509) + ".png"
		for _, c := range []struct {
			name, wantName, wantType, skip string
			size                           int64
		}{
			{"a.png", "a.png", "image/png", "", 10},
			{"big.png", "big.png", "image/png", "", storage.MaxUploadSize},
			{"big.png", "big.png", "image/png", SkipTooLarge, storage.MaxUploadSize + 1},
			{"x.exe", "x.exe", "", SkipTypeNotAllowed, 10},
			{"notes.txt", "notes.txt", "text/plain", "", 10},
			{long, strings.Repeat("n", 508) + ".png", "image/png", "", 10},
		} {
			v := attachment(asana.Attachment{Name: c.name, Host: "asana", DownloadURL: "https://x", Size: c.size})
			assert.Equal(t, c.skip, v.Skip, c.name)
			assert.Equal(t, c.wantName, v.Name)
			if c.wantType != "" {
				assert.Equal(t, c.wantType, v.ContentType, c.name)
			}
		}
	})

	t.Run("comments", func(t *testing.T) {
		for _, c := range []struct {
			name     string
			by       *asana.User
			html     string
			author   string
			stored   string
			prefixed bool
		}{
			{"resolved author", &convUsers[1], "<body>hello</body>", "hanako@example.com", "hello", false},
			{"author outside the project", &convUsers[4], "<body>hello</body>", "", "**Saburo:** hello", true},
			{"author without an account", &convUsers[3], "<body>hello</body>", "", "**Taro:** hello", true},
			{"no author", nil, "<body>hello</body>", "", "**unknown user:** hello", true},
		} {
			v := testConv(t, nil).comment(asana.Story{HTMLText: c.html, CreatedBy: c.by})
			assert.Equal(t, c.author, v.Author, c.name)
			body, _ := v.body(c.prefixed)
			assert.Equal(t, c.stored, body, c.name)
		}
		v := testConv(t, nil).comment(asana.Story{HTMLText: "<body>" + strings.Repeat("a", 50001) + "</body>"})
		body, truncated := v.body(false)
		assert.True(t, truncated)
		assert.Equal(t, 50000, len([]rune(body)))
		assert.Empty(t, testConv(t, nil).comment(asana.Story{HTMLText: "<body></body>"}).Markdown)
	})

	t.Run("people", func(t *testing.T) {
		pc := testConv(t, map[string]string{"101": "Jiro@example.com", "102": "ops@example.com"})
		for _, c := range []struct{ gid, kind, email string }{
			{"101", PersonNotMember, "jiro@example.com"}, // explicit wins over the Asana email
			{"102", PersonExplicit, "ops@example.com"},
			{"100", PersonEmail, "ops@example.com"},
			{"104", PersonNotMember, "saburo@example.com"},
			{"103", PersonUnmatched, ""},
		} {
			kind, email := pc.classify(c.gid)
			assert.Equal(t, c.kind, kind, c.gid)
			assert.Equal(t, c.email, email, c.gid)
		}
		name, ok := pc.people.UserName("101")
		assert.True(t, ok)
		assert.Equal(t, "Hanako", name)
	})
}
