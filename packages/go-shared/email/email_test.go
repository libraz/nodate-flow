package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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

// TestMessage_LogValue_ExcludesBody asserts a Message logged through a
// real slog JSON handler never serialises Body or ReplyTo. The body is
// expected to carry magic-link tokens; the reply-to carries an opaque
// task routing token. Both must be unreachable through slog.Any.
func TestMessage_LogValue_ExcludesBody(t *testing.T) {
	t.Parallel()

	const secretBody = "click https://example.com/verify?t=super-secret-magic-token-xyz"
	const secretReplyTo = "task-routing-token-abcdef"

	m := Message{
		From:    "no-reply@example.com",
		To:      []string{"alice@example.com", "bob@example.com"},
		Subject: "Verify your email",
		Body:    secretBody,
		ReplyTo: secretReplyTo,
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger.LogAttrs(context.Background(), slog.LevelInfo, "send", slog.Any("msg", m))

	out := buf.String()
	if strings.Contains(out, secretBody) {
		t.Fatalf("body leaked into log output: %s", out)
	}
	if strings.Contains(out, "super-secret-magic-token-xyz") {
		t.Fatalf("body token leaked into log output: %s", out)
	}
	if strings.Contains(out, secretReplyTo) {
		t.Fatalf("reply-to token leaked into log output: %s", out)
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("invalid json output: %v: %s", err, out)
	}
	msgGroup, ok := rec["msg"].(map[string]any)
	if !ok {
		t.Fatalf("msg attr not a group: %#v", rec["msg"])
	}
	if _, ok := msgGroup["body"]; ok {
		t.Fatalf("msg group must not contain body: %#v", msgGroup)
	}
	if _, ok := msgGroup["reply_to"]; ok {
		t.Fatalf("msg group must not contain reply_to: %#v", msgGroup)
	}
	if got, want := msgGroup["subject_len"], float64(len("Verify your email")); got != want {
		t.Fatalf("subject_len mismatch: got %v, want %v", got, want)
	}
	if got, want := msgGroup["body_bytes"], float64(len(secretBody)); got != want {
		t.Fatalf("body_bytes mismatch: got %v, want %v", got, want)
	}
}
