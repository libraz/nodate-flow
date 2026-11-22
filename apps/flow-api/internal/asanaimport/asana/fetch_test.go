package asana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/workspaces/w1/users":
			writeJSON(w, 200, data([]User{{GID: "u1", Name: "A", Email: "a@example.com"}}))
		case p == "/workspaces/w1/tags":
			writeJSON(w, 200, data([]Tag{{GID: "tg", Name: "bug"}}))
		case p == "/workspaces/w1/projects":
			writeJSON(w, 200, data([]Project{{GID: "p1", Name: "P1"}, {GID: "p2", Name: "Old", Archived: true}}))
		case p == "/projects/p1/sections":
			writeJSON(w, 200, data([]Section{{GID: "s1", Name: "Todo"}}))
		case p == "/projects/p1/tasks":
			if r.URL.Query().Get("completed_since") != "1970-01-01T00:00:00Z" {
				t.Error("missing completed_since")
			}
			// t1 is repeated within the listing.
			writeJSON(w, 200, data([]Task{{GID: "t1", Name: "one", NumSubtasks: 2}, {GID: "t1"}, {GID: "t3"}}))
		case p == "/tasks/t1/subtasks":
			// t2 is repeated; t3 is also directly listed.
			writeJSON(w, 200, data([]Task{{GID: "t2", NumSubtasks: 1}, {GID: "t2"}, {GID: "t3"}}))
		case p == "/tasks/t2/subtasks":
			writeJSON(w, 200, data([]Task{{GID: "t4"}}))
		case strings.HasSuffix(p, "/stories"):
			writeJSON(w, 200, data([]Story{
				{GID: "c-" + p, ResourceSubtype: "comment_added", HTMLText: "<body>hi</body>", NumLikes: 2},
				{GID: "sys", ResourceSubtype: "assigned"},
				{GID: "sys2", ResourceSubtype: "marked_complete"},
			}))
		case p == "/attachments":
			writeJSON(w, 200, data([]Attachment{{GID: "a-" + r.URL.Query().Get("parent"), Host: "asana"}}))
		default:
			t.Errorf("unexpected request %s", r.URL)
			writeJSON(w, 404, map[string]any{})
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	snap, err := c.Fetch(context.Background(), Scope{WorkspaceGID: "w1", ProjectGIDs: []string{"p1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Users) != 1 || len(snap.Tags) != 1 || len(snap.Projects) != 2 || !snap.Projects[1].Archived {
		t.Fatalf("workspace lists: %+v", snap)
	}
	ps := snap.Mapped["p1"]
	if ps == nil || len(ps.Sections) != 1 || ps.Project.Name != "P1" {
		t.Fatalf("project: %+v", ps)
	}
	if got := strings.Join(ps.Order, ","); got != "t1,t2,t4,t3" {
		t.Fatalf("order %s", got)
	}
	if !ps.Direct["t1"] || !ps.Direct["t3"] || ps.Direct["t2"] || ps.Direct["t4"] {
		t.Fatalf("direct %v", ps.Direct)
	}
	wantRep := []Repeat{{GID: "t1"}, {Parent: "t1", GID: "t2"}}
	if len(ps.Repeated) != 2 || ps.Repeated[0] != wantRep[0] || ps.Repeated[1] != wantRep[1] {
		t.Fatalf("repeated %v", ps.Repeated)
	}
	if len(snap.Tasks) != 4 {
		t.Fatalf("tasks %d", len(snap.Tasks))
	}
	d := snap.Tasks["t4"]
	if len(d.Comments) != 1 || d.SystemStories != 2 || d.Likes != 2 || len(d.Attachments) != 1 || d.Attachments[0].GID != "a-t4" {
		t.Fatalf("detail %+v", d)
	}
}

func TestFetchErrorIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/workspaces/w1/users" {
			writeJSON(w, 403, map[string]any{})
			return
		}
		writeJSON(w, 200, data([]Tag{}))
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	if snap, err := c.Fetch(context.Background(), Scope{WorkspaceGID: "w1"}); err == nil || snap != nil {
		t.Fatalf("want error, got %v %v", snap, err)
	}
}
