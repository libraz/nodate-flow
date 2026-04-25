package obs

import "testing"

func TestDomainForRoute(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/workspaces/{wsId}/calendars", "calendar"},
		{"/workspaces/{wsId}/calendars/{calId}/events", "calendar"},
		{"/share/cal/{token}", "calendar"},
		{"/invites/{token}/info", "calendar"},
		{"/me/invites", "calendar"},
		{"/workspaces/{wsId}/tasks", "task"},
		{"/health", "task"},
	}
	for _, c := range cases {
		if got := domainForRoute(c.in); got != c.want {
			t.Errorf("domainForRoute(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
