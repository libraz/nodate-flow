package tasks

import (
	"database/sql"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

func TestTaskAcceptsAgentWork(t *testing.T) {
	open := generated.FindTaskByPublicIdRow{
		Visibility:   generated.TasksVisibilityPublic,
		DerivedState: generated.TasksDerivedStateOpen,
	}
	cases := []struct {
		name string
		edit func(*generated.FindTaskByPublicIdRow)
		want bool
	}{
		{"public and open", func(*generated.FindTaskByPublicIdRow) {}, true},
		{"project visibility", func(r *generated.FindTaskByPublicIdRow) { r.Visibility = generated.TasksVisibilityProject }, false},
		{"private", func(r *generated.FindTaskByPublicIdRow) { r.Visibility = generated.TasksVisibilityPrivate }, false},
		{"done", func(r *generated.FindTaskByPublicIdRow) { r.DerivedState = generated.TasksDerivedStateDone }, false},
		{"cancelled", func(r *generated.FindTaskByPublicIdRow) { r.DerivedState = generated.TasksDerivedStateCancelled }, false},
		{"archived", func(r *generated.FindTaskByPublicIdRow) {
			r.ArchivedAt = sql.NullTime{Time: time.Now(), Valid: true}
		}, false},
	}
	for _, c := range cases {
		row := open
		c.edit(&row)
		if got := taskAcceptsAgentWork(row); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHasGrants(t *testing.T) {
	cases := map[string]bool{
		``:                            false,
		`null`:                        false,
		`{}`:                          false,
		`["add_comment"]`:             false,
		`{"add_comment":"auto"}`:      true,
		`{"add_comment":"bogus"}`:     false,
		`{"set_priority":"approval"}`: true,
	}
	for raw, want := range cases {
		if got := hasGrants([]byte(raw)); got != want {
			t.Errorf("hasGrants(%q) = %v, want %v", raw, got, want)
		}
	}
}
