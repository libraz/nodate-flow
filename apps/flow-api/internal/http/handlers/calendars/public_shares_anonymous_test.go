package calendars

import "testing"

func TestApplyAnonymousViewWithholdsMemoAndURLEverywhere(t *testing.T) {
	t.Parallel()
	memo, url, loc := "memo", "https://example.test", "Room 1"
	cases := []struct {
		name            string
		visibility      string
		calendarDefault string
		wantTitle       string
		wantLocation    bool
	}{
		{"public", "public", "", "Plan", true},
		{"default on a public calendar", "default", "public", "Plan", true},
		{"private", "private", "", "", false},
		{"default on a private calendar", "default", "private", "", false},
	}
	for _, c := range cases {
		ev := PublicShareRenderEvent{Title: "Plan", Location: &loc, Memo: &memo, URL: &url}
		applyAnonymousView(&ev, c.visibility, c.calendarDefault)
		if ev.Memo != nil || ev.URL != nil {
			t.Errorf("%s: memo/url leaked to an anonymous reader: %v %v", c.name, ev.Memo, ev.URL)
		}
		if ev.Title != c.wantTitle {
			t.Errorf("%s: title = %q, want %q", c.name, ev.Title, c.wantTitle)
		}
		if (ev.Location != nil) != c.wantLocation {
			t.Errorf("%s: location present = %v, want %v", c.name, ev.Location != nil, c.wantLocation)
		}
	}
}
