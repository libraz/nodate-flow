package github

import "testing"

func TestNormalizeEventKind(t *testing.T) {
	cases := []struct {
		ev, action, want string
	}{
		{"pull_request", "opened", "pull_request.opened"},
		{"check_run", "completed", "check_run.completed"},
		{"deployment_status", "", "deployment_status"},
		{"", "anything", "unknown"},
	}
	for _, c := range cases {
		got := NormalizeEventKind(c.ev, c.action)
		if got != c.want {
			t.Errorf("NormalizeEventKind(%q,%q) = %q, want %q", c.ev, c.action, got, c.want)
		}
	}
}
