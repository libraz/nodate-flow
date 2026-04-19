package email

import (
	"context"
	"errors"
	"testing"
)

func TestNoopSender(t *testing.T) {
	if err := (NoopSender{}).Send(context.Background(), Message{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestMemorySenderRecords(t *testing.T) {
	s := &MemorySender{}
	_ = s.Send(context.Background(), Message{To: []string{"a@b"}, Subject: "hi"})
	_ = s.Send(context.Background(), Message{To: []string{"c@d"}, Subject: "yo"})
	if len(s.Sent) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.Sent))
	}
	if s.Sent[1].Subject != "yo" {
		t.Fatalf("unexpected order: %+v", s.Sent)
	}
}
