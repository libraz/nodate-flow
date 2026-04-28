// Package email provides an outbound transport interface for sending
// email messages. Implementations send a single message; inbound
// parsing is out of scope.
package email

import (
	"context"
	"errors"
	"log/slog"
)

// Message is the minimal structured representation of an outbound
// email. Body is plain text; HTML is intentionally not supported in
// the v1 surface to keep the deliverability story simple.
type Message struct {
	From    string
	To      []string
	Subject string
	Body    string
	// ReplyTo carries an opaque routing token (e.g. base32 task id)
	// the inbound parser uses to attribute replies back to a task.
	ReplyTo string
}

// LogValue implements [slog.LogValuer] so a Message logged via slog
// only emits non-sensitive metadata. Body is intentionally excluded
// because it can carry magic-link tokens, calendar invite links, and
// verification codes; ReplyTo is excluded because it carries an
// opaque routing token that points at a task. The reported recipients,
// subject_len, and body_bytes give operators enough signal to triage
// delivery without exposing payload.
func (m Message) LogValue() slog.Value {
	to := make([]string, len(m.To))
	copy(to, m.To)
	return slog.GroupValue(
		slog.String("from", m.From),
		slog.Any("to", to),
		slog.Int("subject_len", len(m.Subject)),
		slog.Int("body_bytes", len(m.Body)),
	)
}

// Sender is the egress contract. Implementations are typically
// SMTP-backed in prod and a stub queue in tests.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrNotConfigured is returned when no SMTP transport is wired up.
var ErrNotConfigured = errors.New("email: sender not configured")

// NoopSender is the default Sender for environments where outbound
// email is intentionally disabled. Every Send returns
// [ErrNotConfigured] so callers can decide whether the failure is
// fatal.
type NoopSender struct{}

// Send returns ErrNotConfigured.
func (NoopSender) Send(_ context.Context, _ Message) error { return ErrNotConfigured }

// MemorySender records every Send for unit tests.
type MemorySender struct {
	Sent []Message
}

// Send appends m to Sent and returns nil.
func (s *MemorySender) Send(_ context.Context, m Message) error {
	s.Sent = append(s.Sent, m)
	return nil
}
