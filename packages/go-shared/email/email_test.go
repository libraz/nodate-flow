package email

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestSMTPHeaderEncoding(t *testing.T) {
	t.Parallel()

	got := encodeHeader("日本語タイトル")
	if got == "日本語タイトル" {
		t.Fatalf("non-ASCII header was not MIME encoded")
	}
	if !strings.HasPrefix(got, "=?utf-8?q?") {
		t.Fatalf("unexpected encoded header %q", got)
	}
}

func TestSMTPHeaderValueRejectsCRLF(t *testing.T) {
	t.Parallel()

	if err := validateHeaderValues("safe subject"); err != nil {
		t.Fatalf("safe header value rejected: %v", err)
	}
	if err := validateHeaderValues("safe\r\nBcc: attacker@example.test"); err == nil {
		t.Fatalf("header value containing CRLF should be rejected")
	}
}

func TestSMTPSenderDeliversRFC5322MessageToServer(t *testing.T) {
	t.Parallel()

	srv := newTestSMTPServer(t)
	sender, err := NewSMTPSender(SMTPConfig{
		Host: "127.0.0.1",
		Port: srv.port,
		From: "fallback@example.test",
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}

	err = sender.Send(context.Background(), Message{
		From:    "sender@example.test",
		To:      []string{"alice@example.test", "bob@example.test"},
		ReplyTo: "reply@example.test",
		Subject: "日本語タイトル",
		Body:    "Plain text body\nsecond line",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.message(t)

	if got.mailFrom != "FROM:<sender@example.test>" {
		t.Fatalf("MAIL command = %q", got.mailFrom)
	}
	if len(got.rcptTo) != 2 {
		t.Fatalf("RCPT commands = %#v", got.rcptTo)
	}
	if got.rcptTo[0] != "TO:<alice@example.test>" || got.rcptTo[1] != "TO:<bob@example.test>" {
		t.Fatalf("unexpected RCPT commands: %#v", got.rcptTo)
	}
	for _, want := range []string{
		"From: sender@example.test\r\n",
		"To: alice@example.test, bob@example.test\r\n",
		"Reply-To: reply@example.test\r\n",
		"Subject: =?utf-8?q?",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"\r\nPlain text body\r\nsecond line",
	} {
		if !strings.Contains(got.data, want) {
			t.Fatalf("SMTP DATA missing %q in:\n%s", want, got.data)
		}
	}
}

func TestSMTPSenderEncodesDisplayNamesPerAddress(t *testing.T) {
	t.Parallel()

	srv := newTestSMTPServer(t)
	sender, err := NewSMTPSender(SMTPConfig{
		Host: "127.0.0.1",
		Port: srv.port,
		From: "fallback@example.test",
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}

	err = sender.Send(context.Background(), Message{
		From:    "送信者 <sender@example.test>",
		To:      []string{"太郎 <taro@example.test>", "花子 <hanako@example.test>"},
		ReplyTo: "返信先 <reply@example.test>",
		Subject: "hello",
		Body:    "body",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.message(t)

	if got.mailFrom != "FROM:<sender@example.test>" {
		t.Fatalf("MAIL command = %q", got.mailFrom)
	}
	if got.rcptTo[0] != "TO:<taro@example.test>" || got.rcptTo[1] != "TO:<hanako@example.test>" {
		t.Fatalf("unexpected RCPT commands: %#v", got.rcptTo)
	}
	for _, want := range []string{
		"From: =?utf-8?q?",
		" <sender@example.test>\r\n",
		"To: =?utf-8?q?",
		" <taro@example.test>, =?utf-8?q?",
		" <hanako@example.test>\r\n",
		"Reply-To: =?utf-8?q?",
		" <reply@example.test>\r\n",
	} {
		if !strings.Contains(got.data, want) {
			t.Fatalf("SMTP DATA missing %q in:\n%s", want, got.data)
		}
	}
}

func TestSMTPSenderAuthenticatesWhenConfigured(t *testing.T) {
	t.Parallel()

	srv := newTestSMTPServer(t)
	sender, err := NewSMTPSender(SMTPConfig{
		Host:     "127.0.0.1",
		Port:     srv.port,
		Username: "smtp-user",
		Password: "smtp-pass",
		From:     "fallback@example.test",
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if err := sender.Send(context.Background(), Message{
		To:      []string{"alice@example.test"},
		Subject: "auth path",
		Body:    "authenticated delivery",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := srv.message(t)
	if got.authPlain != "\x00smtp-user\x00smtp-pass" {
		t.Fatalf("AUTH PLAIN payload = %q", got.authPlain)
	}
	if got.mailFrom != "FROM:<fallback@example.test>" {
		t.Fatalf("MAIL command = %q", got.mailFrom)
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

type capturedSMTPMessage struct {
	mailFrom  string
	rcptTo    []string
	data      string
	authPlain string
}

type testSMTPServer struct {
	port int
	done chan capturedSMTPMessage
}

func newTestSMTPServer(t *testing.T) *testSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test SMTP: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	_, portRaw, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}

	s := &testSMTPServer{port: port, done: make(chan capturedSMTPMessage, 1)}
	var once sync.Once
	fail := func(err error) {
		once.Do(func() {
			t.Logf("test SMTP server error: %v", err)
			s.done <- capturedSMTPMessage{}
		})
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			fail(err)
			return
		}
		defer func() { _ = conn.Close() }()
		msg, err := serveOneSMTP(conn)
		if err != nil {
			fail(err)
			return
		}
		once.Do(func() { s.done <- msg })
	}()
	return s
}

func (s *testSMTPServer) message(t *testing.T) capturedSMTPMessage {
	t.Helper()
	var msg capturedSMTPMessage
	select {
	case msg = <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for test SMTP server")
	}
	if msg.data == "" {
		t.Fatal("test SMTP server did not capture DATA")
	}
	return msg
}

func serveOneSMTP(conn net.Conn) (capturedSMTPMessage, error) {
	var msg capturedSMTPMessage
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return msg, err
	}
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(format string, args ...any) error {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return err
		}
		return w.Flush()
	}
	if err := write("220 test-smtp ESMTP\r\n"); err != nil {
		return msg, err
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return msg, err
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO ") || strings.HasPrefix(upper, "HELO "):
			if err := write("250-test-smtp\r\n250-AUTH PLAIN\r\n250 OK\r\n"); err != nil {
				return msg, err
			}
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			encoded := strings.TrimSpace(line[len("AUTH PLAIN"):])
			if encoded == "" {
				if err := write("334 \r\n"); err != nil {
					return msg, err
				}
				encodedLine, err := r.ReadString('\n')
				if err != nil {
					return msg, err
				}
				encoded = strings.TrimRight(encodedLine, "\r\n")
			}
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return msg, err
			}
			msg.authPlain = string(raw)
			if err := write("235 Authentication successful\r\n"); err != nil {
				return msg, err
			}
		case strings.HasPrefix(upper, "MAIL "):
			msg.mailFrom = strings.TrimSpace(line[len("MAIL "):])
			if err := write("250 OK\r\n"); err != nil {
				return msg, err
			}
		case strings.HasPrefix(upper, "RCPT "):
			msg.rcptTo = append(msg.rcptTo, strings.TrimSpace(line[len("RCPT "):]))
			if err := write("250 OK\r\n"); err != nil {
				return msg, err
			}
		case upper == "DATA":
			if err := write("354 End data with <CR><LF>.<CR><LF>\r\n"); err != nil {
				return msg, err
			}
			var b strings.Builder
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return msg, err
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				b.WriteString(dataLine)
			}
			msg.data = b.String()
			if err := write("250 OK queued\r\n"); err != nil {
				return msg, err
			}
		case upper == "QUIT":
			_ = write("221 Bye\r\n")
			return msg, nil
		default:
			if err := write("250 OK\r\n"); err != nil {
				return msg, err
			}
		}
	}
}
