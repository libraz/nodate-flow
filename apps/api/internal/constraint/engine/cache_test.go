package engine

import (
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/constraint"
)

func TestCache_Memoizes(t *testing.T) {
	c := NewCache()
	expr := []byte(`{"op":"actor.has_role","arg":"reviewer"}`)
	f := constraint.Facts{ActorRoles: map[string]bool{"reviewer": true}}
	v1, _ := c.Evaluate(expr, f)
	v2, _ := c.Evaluate(expr, f)
	if v1 != true || v2 != true {
		t.Fatal("expected true both times")
	}
	if c.Size() != 1 {
		t.Fatalf("expected 1 cache entry, got %d", c.Size())
	}
}

func TestCache_DifferentFactsDifferentEntries(t *testing.T) {
	c := NewCache()
	expr := []byte(`{"op":"actor.has_role","arg":"reviewer"}`)
	_, _ = c.Evaluate(expr, constraint.Facts{ActorRoles: map[string]bool{"reviewer": true}})
	_, _ = c.Evaluate(expr, constraint.Facts{ActorRoles: map[string]bool{"author": true}})
	if c.Size() != 2 {
		t.Fatalf("expected 2 cache entries, got %d", c.Size())
	}
}

func TestCache_MapOrderIndependent(t *testing.T) {
	// Two Facts with the same logical content in different
	// insertion orders must hash to the same key.
	c := NewCache()
	expr := []byte(`{"op":"dependency.all_done","taskIds":["a","b"]}`)
	f1 := constraint.Facts{DependencyStates: map[string]string{"a": "done", "b": "done"}}
	f2 := constraint.Facts{DependencyStates: map[string]string{"b": "done", "a": "done"}}
	_, _ = c.Evaluate(expr, f1)
	_, _ = c.Evaluate(expr, f2)
	if c.Size() != 1 {
		t.Fatalf("expected 1 cache entry (order-independent), got %d", c.Size())
	}
}

func TestCache_ParseErrorNotCached(t *testing.T) {
	c := NewCache()
	_, err := c.Evaluate([]byte(`{"op":"bogus"}`), constraint.Facts{})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if c.Size() != 0 {
		t.Fatal("parse errors must not be cached")
	}
}
