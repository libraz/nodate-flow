package nlconstraint

import (
	"context"
	"testing"
)

func TestCompile_Mock(t *testing.T) {
	c := New(NewMockProvider())
	parsed, err := c.Compile(context.Background(), "Due before next Monday")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Op != "time.due_before" || parsed.Arg == "" {
		t.Fatalf("unexpected parse: %+v", parsed)
	}
}

func TestCompile_Unknown(t *testing.T) {
	c := New(NewMockProvider())
	if _, err := c.Compile(context.Background(), "nonsense prompt with no match"); err != ErrUnparseable {
		t.Fatalf("expected ErrUnparseable, got %v", err)
	}
}

func TestCompile_EmptyPrompt(t *testing.T) {
	c := New(NewMockProvider())
	if _, err := c.Compile(context.Background(), "   "); err != ErrUnparseable {
		t.Fatalf("expected ErrUnparseable on empty, got %v", err)
	}
}

func TestCompile_Composite(t *testing.T) {
	c := New(NewMockProvider())
	parsed, err := c.Compile(context.Background(), "CI green and PR merged")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Op != "and" || len(parsed.Terms) != 2 {
		t.Fatalf("unexpected parse: %+v", parsed)
	}
}
