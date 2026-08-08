package logutil

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// internalIDSentinel is a value distinctive enough that finding it
// anywhere in a rendered log line proves a leak, and short enough to be
// a plausible row id.
const internalIDSentinel = 918273

// captureJSON renders one log record through RedactHandler and returns
// both the raw line and the decoded object.
func captureJSON(t *testing.T, emit func(l *slog.Logger)) (string, map[string]any) {
	t.Helper()

	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	emit(slog.New(NewRedactHandler(base)))

	raw := buf.String()
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("invalid json: %v: %s", err, raw)
	}
	return raw, rec
}

// TestSlogNormalisesIntegerWidths pins the assumption RedactHandler's
// internal-id check rests on: slog folds every integer width down to
// KindInt64 / KindUint64, so a handler that inspects those two kinds sees
// every numeric attr regardless of how the call site spelled it. If a
// future Go release stops normalising, this fails before the leak does.
func TestSlogNormalisesIntegerWidths(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		value any
		want  slog.Kind
	}{
		"int":    {int(1), slog.KindInt64},
		"int32":  {int32(1), slog.KindInt64},
		"int64":  {int64(1), slog.KindInt64},
		"uint":   {uint(1), slog.KindUint64},
		"uint32": {uint32(1), slog.KindUint64},
		"uint64": {uint64(1), slog.KindUint64},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := slog.AnyValue(tc.value).Kind(); got != tc.want {
				t.Fatalf("slog.AnyValue(%T) kind: got %v want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestRedactHandler_SuppressesInternalIDs walks every spelling a caller
// can reach the handler through. The Attr-constructor form is the one the
// lint rule catches; the rest are the forms it structurally cannot see,
// which is why the handler exists.
func TestRedactHandler_SuppressesInternalIDs(t *testing.T) {
	t.Parallel()

	sentinel := strconv.Itoa(internalIDSentinel)

	cases := map[string]struct {
		emit func(l *slog.Logger)
		key  string
	}{
		"slog.Int64 attr": {
			emit: func(l *slog.Logger) {
				l.Info("msg", slog.Int64("workspace_id", internalIDSentinel))
			},
			key: "workspace_id",
		},
		"slog.Uint64 attr": {
			emit: func(l *slog.Logger) {
				l.Info("msg", slog.Uint64("workspace_internal", internalIDSentinel))
			},
			key: "workspace_internal",
		},
		"slog.Int attr": {
			emit: func(l *slog.Logger) {
				l.Info("msg", slog.Int("agent_id", internalIDSentinel))
			},
			key: "agent_id",
		},
		// The escape hatch a call site actually used to dodge the lint
		// rule: slog.Any is not one of the forbidden constructors.
		"slog.Any with uint32": {
			emit: func(l *slog.Logger) {
				l.Info("msg", slog.Any("actor_id", uint32(internalIDSentinel)))
			},
			key: "actor_id",
		},
		// The loose key/value form, which has no callee to match at all.
		"loose key/value pair": {
			emit: func(l *slog.Logger) {
				l.Warn("msg", "workspace_id", uint32(internalIDSentinel))
			},
			key: "workspace_id",
		},
		// Attrs bound onto the logger up front, i.e. the request-scoped
		// logger built by the LoggerContext middleware.
		"WithAttrs": {
			emit: func(l *slog.Logger) {
				l.With(slog.Any("workspace_id", uint32(internalIDSentinel))).Info("msg")
			},
			key: "workspace_id",
		},
		"bare id key": {
			emit: func(l *slog.Logger) {
				l.Info("msg", slog.Int64("id", internalIDSentinel))
			},
			key: "id",
		},
		"suffixed internal id key": {
			emit: func(l *slog.Logger) {
				l.Info("msg", slog.Uint64("user_internal_id", internalIDSentinel))
			},
			key: "user_internal_id",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, rec := captureJSON(t, tc.emit)
			if strings.Contains(raw, sentinel) {
				t.Fatalf("internal id %s reached the log line: %s", sentinel, raw)
			}
			got, ok := rec[tc.key].(string)
			if !ok {
				t.Fatalf("%s missing or not a string: %s", tc.key, raw)
			}
			if got != InternalIDPlaceholder {
				t.Fatalf("%s: got %q want %q", tc.key, got, InternalIDPlaceholder)
			}
		})
	}
}

// TestRedactHandler_SuppressesInternalIDsInGroups covers grouped attrs,
// where the id-shaped key is the inner one.
func TestRedactHandler_SuppressesInternalIDsInGroups(t *testing.T) {
	t.Parallel()

	raw, rec := captureJSON(t, func(l *slog.Logger) {
		l.Info("msg", slog.Group("workspace",
			slog.Int64("id", internalIDSentinel),
			slog.String("name", "acme"),
		))
	})
	if strings.Contains(raw, strconv.Itoa(internalIDSentinel)) {
		t.Fatalf("internal id reached the log line: %s", raw)
	}
	group, ok := rec["workspace"].(map[string]any)
	if !ok {
		t.Fatalf("workspace group missing: %s", raw)
	}
	if group["id"] != InternalIDPlaceholder {
		t.Fatalf("workspace.id: got %v want %q", group["id"], InternalIDPlaceholder)
	}
	if group["name"] != "acme" {
		t.Fatalf("workspace.name was disturbed: got %v", group["name"])
	}
}

// idValuer is an slog.LogValuer that resolves to a bare row id, the one
// shape that would otherwise slip past a check made before resolution.
type idValuer struct{ id int64 }

func (v idValuer) LogValue() slog.Value { return slog.Int64Value(v.id) }

// TestRedactHandler_SuppressesInternalIDsFromLogValuer asserts the check
// is re-applied after LogValuer resolution.
func TestRedactHandler_SuppressesInternalIDsFromLogValuer(t *testing.T) {
	t.Parallel()

	raw, rec := captureJSON(t, func(l *slog.Logger) {
		l.Info("msg", slog.Any("task_id", idValuer{id: internalIDSentinel}))
	})
	if strings.Contains(raw, strconv.Itoa(internalIDSentinel)) {
		t.Fatalf("internal id reached the log line: %s", raw)
	}
	if rec["task_id"] != InternalIDPlaceholder {
		t.Fatalf("task_id: got %v want %q", rec["task_id"], InternalIDPlaceholder)
	}
}

// TestRedactHandler_KeepsLegitimateAttrs is the false-positive half. A
// guard that eats real fields is its own defect, so the values operators
// depend on are pinned here: string correlation ids, public ids, and any
// numeric attr that is not id-shaped.
func TestRedactHandler_KeepsLegitimateAttrs(t *testing.T) {
	t.Parallel()

	pub := uuid.MustParse("01890ec3-1a2b-7cde-9f01-23456789abcd")

	_, rec := captureJSON(t, func(l *slog.Logger) {
		l.Info("msg",
			slog.String("request_id", "req-abc"),
			slog.String("session_id", "sess-xyz"),
			LogEntity("workspace", pub),
			slog.String("workspace_public_id", pub.String()),
			slog.Int("status", 404),
			slog.Int64("duration_ms", 128),
			slog.Int("rows_written", 17),
			LogNumber("members_total", 3),
		)
	})

	wantString := map[string]string{
		"request_id":          "req-abc",
		"session_id":          "sess-xyz",
		"workspace_public_id": pub.String(),
	}
	for k, want := range wantString {
		if rec[k] != want {
			t.Fatalf("%s: got %v want %q", k, rec[k], want)
		}
	}
	wantNumber := map[string]float64{
		"status":        404,
		"duration_ms":   128,
		"rows_written":  17,
		"members_total": 3,
	}
	for k, want := range wantNumber {
		if rec[k] != want {
			t.Fatalf("%s: got %v (%T) want %v", k, rec[k], rec[k], want)
		}
	}
}

// TestIsInternalIDKey enumerates the key shapes the predicate has to
// separate, including the "_public_id" exemption that keeps [LogEntity]
// output intact.
func TestIsInternalIDKey(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"id":                   true,
		"workspace_id":         true,
		"task_id":              true,
		"actor_id":             true,
		"storage_object_id":    true,
		"workspace_internal":   true,
		"signal_internal":      true,
		"user_internal_id":     true,
		"source_task_internal": true,
		"WORKSPACE_ID":         true,
		"workspace_public_id":  false,
		"task_public_id":       false,
		"status":               false,
		"duration_ms":          false,
		"rows_written":         false,
		"members_total":        false,
		"identity":             false,
		"invalid":              false,
	}
	for key, want := range cases {
		if got := IsInternalIDKey(key); got != want {
			t.Fatalf("IsInternalIDKey(%q): got %v want %v", key, got, want)
		}
	}
}

// TestLogNumber_RejectsIDShapedKeys asserts the sanctioned numeric helper
// cannot be used to re-open the hole the lint ban closes.
func TestLogNumber_RejectsIDShapedKeys(t *testing.T) {
	t.Parallel()

	attr := LogNumber("task_id", internalIDSentinel)
	if attr.Value.Kind() != slog.KindString {
		t.Fatalf("value kind: got %v want String", attr.Value.Kind())
	}
	if attr.Value.String() != InternalIDPlaceholder {
		t.Fatalf("value: got %q want %q", attr.Value.String(), InternalIDPlaceholder)
	}
}

// TestLogNumber_AcceptsEveryIntegerWidth covers the generic type set, so
// call sites can pass len(), a uint32 column, or an int64 COUNT(*)
// without a conversion.
func TestLogNumber_AcceptsEveryIntegerWidth(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{
		LogNumber("a", 1),
		LogNumber("b", int32(2)),
		LogNumber("c", int64(3)),
		LogNumber("d", uint(4)),
		LogNumber("e", uint32(5)),
		LogNumber("f", uint64(6)),
	}
	for i, attr := range attrs {
		if attr.Value.Kind() != slog.KindInt64 {
			t.Fatalf("attr %d kind: got %v want Int64", i, attr.Value.Kind())
		}
		if want := int64(i + 1); attr.Value.Int64() != want {
			t.Fatalf("attr %d value: got %d want %d", i, attr.Value.Int64(), want)
		}
	}
}
