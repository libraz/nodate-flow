package logutil

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// TestLogEntity_KeyAndValue asserts the helper produces an slog.Attr
// keyed "<name>_public_id" carrying the canonical UUID string form.
func TestLogEntity_KeyAndValue(t *testing.T) {
	t.Parallel()

	u := uuid.MustParse("01890ec3-1a2b-7cde-9f01-23456789abcd")

	attr := LogEntity("workspace", u)
	if attr.Key != "workspace_public_id" {
		t.Fatalf("key: got %q want %q", attr.Key, "workspace_public_id")
	}
	if attr.Value.Kind() != slog.KindString {
		t.Fatalf("value kind: got %v want String", attr.Value.Kind())
	}
	if attr.Value.String() != u.String() {
		t.Fatalf("value: got %q want %q", attr.Value.String(), u.String())
	}
}

// TestLogEntityPID_KeyAndValue mirrors TestLogEntity_KeyAndValue for the
// dbtype.PublicID input flavour.
func TestLogEntityPID_KeyAndValue(t *testing.T) {
	t.Parallel()

	u := uuid.MustParse("01890ec3-1a2b-7cde-9f01-23456789abcd")
	pid := dbtype.FromUUID(u)

	attr := LogEntityPID("task", pid)
	if attr.Key != "task_public_id" {
		t.Fatalf("key: got %q want %q", attr.Key, "task_public_id")
	}
	if attr.Value.Kind() != slog.KindString {
		t.Fatalf("value kind: got %v want String", attr.Value.Kind())
	}
	if attr.Value.String() != pid.String() {
		t.Fatalf("value: got %q want %q", attr.Value.String(), pid.String())
	}
	if attr.Value.String() != u.String() {
		t.Fatalf("value not canonical UUID: got %q want %q", attr.Value.String(), u.String())
	}
}

// TestLogEntity_DifferentNames covers the per-domain key naming.
func TestLogEntity_DifferentNames(t *testing.T) {
	t.Parallel()

	u := uuid.New()
	cases := []string{"workspace", "task", "user", "actor", "project", "page", "label", "comment", "event", "calendar", "share", "invite"}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := LogEntity(name, u)
			want := name + "_public_id"
			if got.Key != want {
				t.Fatalf("key: got %q want %q", got.Key, want)
			}
		})
	}
}

// TestLogEntity_RoundtripsThroughSlog asserts the helper is wire-compatible
// with the project's RedactHandler + JSON output.
func TestLogEntity_RoundtripsThroughSlog(t *testing.T) {
	t.Parallel()

	u := uuid.MustParse("01890ec3-1a2b-7cde-9f01-23456789abcd")

	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := slog.New(NewRedactHandler(base))
	l.Info("test", LogEntity("task", u))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("invalid json: %v: %s", err, buf.String())
	}
	got, ok := rec["task_public_id"].(string)
	if !ok {
		t.Fatalf("task_public_id not present or not a string: %s", buf.String())
	}
	if got != u.String() {
		t.Fatalf("value: got %q want %q", got, u.String())
	}
	// Belt-and-braces: numeric form of an internal id must never sneak in.
	if strings.Contains(buf.String(), `"task_id"`) {
		t.Fatalf("internal task_id leaked into log: %s", buf.String())
	}
}
