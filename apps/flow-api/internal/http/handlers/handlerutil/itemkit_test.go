package handlerutil

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

func TestClassifyItemkitErrorNil(t *testing.T) {
	t.Parallel()
	if got := ClassifyItemkitError(nil); got != ItemkitErrorNone {
		t.Errorf("nil: got %v want ItemkitErrorNone", got)
	}
}

func TestClassifyItemkitErrorNoRows(t *testing.T) {
	t.Parallel()
	if got := ClassifyItemkitError(sql.ErrNoRows); got != ItemkitErrorNotFound {
		t.Errorf("ErrNoRows: got %v want ItemkitErrorNotFound", got)
	}
}

func TestClassifyItemkitErrorNotFoundMessage(t *testing.T) {
	t.Parallel()
	if got := ClassifyItemkitError(errors.New("event not found in workspace")); got != ItemkitErrorNotFound {
		t.Errorf("not-found message: got %v want ItemkitErrorNotFound", got)
	}
}

func TestClassifyItemkitErrorRecurrence(t *testing.T) {
	t.Parallel()
	if got := ClassifyItemkitError(errors.New("recurrence forbidden when linked")); got != ItemkitErrorRecurrenceWithLink {
		t.Errorf("recurrence: got %v want ItemkitErrorRecurrenceWithLink", got)
	}
}

func TestClassifyItemkitErrorInvariant(t *testing.T) {
	t.Parallel()
	if got := ClassifyItemkitError(errors.New("itemkit invariant: dual link")); got != ItemkitErrorInvariant {
		t.Errorf("invariant: got %v want ItemkitErrorInvariant", got)
	}
}

func TestClassifyItemkitErrorOther(t *testing.T) {
	t.Parallel()
	if got := ClassifyItemkitError(errors.New("connection refused")); got != ItemkitErrorOther {
		t.Errorf("other: got %v want ItemkitErrorOther", got)
	}
}

// extractCode pulls the apierror code from the canonical envelope so
// tests can assert against the wire type without depending on
// internal pointer identity.
func extractCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var pd *ProblemDetails
	if !errors.As(err, &pd) {
		t.Fatalf("expected *ProblemDetails, got %T", err)
	}
	return pd.Type
}

func TestTranslateCalendarItemkitErrorNil(t *testing.T) {
	t.Parallel()
	if err := TranslateCalendarItemkitError(context.Background(), "op", nil, nil); err != nil {
		t.Errorf("nil should pass through: got %v", err)
	}
}

func TestTranslateCalendarItemkitErrorNotFound(t *testing.T) {
	t.Parallel()
	got := TranslateCalendarItemkitError(context.Background(), "itemkit.RenameItem", sql.ErrNoRows, nil)
	if extractCode(t, got) != apierrors.CalendarEventNotFound.Code {
		t.Errorf("not-found: got %v want %v", extractCode(t, got), apierrors.CalendarEventNotFound.Code)
	}
}

func TestTranslateCalendarItemkitErrorRecurrence(t *testing.T) {
	t.Parallel()
	got := TranslateCalendarItemkitError(context.Background(), "op", errors.New("recurrence forbidden"), nil)
	if extractCode(t, got) != apierrors.ItemItemkitRecurrenceWithTaskLink.Code {
		t.Errorf("recurrence: got %v", extractCode(t, got))
	}
}

func TestTranslateCalendarItemkitErrorInvariant(t *testing.T) {
	t.Parallel()
	got := TranslateCalendarItemkitError(context.Background(), "op", errors.New("itemkit invariant breach"), nil)
	if extractCode(t, got) != apierrors.ItemItemkitInvariantViolation.Code {
		t.Errorf("invariant: got %v", extractCode(t, got))
	}
}

func TestTranslateCalendarItemkitErrorFallbackDelete(t *testing.T) {
	t.Parallel()
	got := TranslateCalendarItemkitError(
		context.Background(),
		"itemkit.DeleteEvent",
		errors.New("connection lost"),
		apierrors.CalendarEventStoreDeleteInterrupted,
	)
	if extractCode(t, got) != apierrors.CalendarEventStoreDeleteInterrupted.Code {
		t.Errorf("delete fallback: got %v", extractCode(t, got))
	}
}

func TestTranslateCalendarItemkitErrorFallbackDefault(t *testing.T) {
	t.Parallel()
	got := TranslateCalendarItemkitError(context.Background(), "op", errors.New("connection lost"), nil)
	if extractCode(t, got) != apierrors.CalendarEventStoreWriteInterrupted.Code {
		t.Errorf("default fallback: got %v", extractCode(t, got))
	}
}

func TestTranslateTaskItemkitErrorNil(t *testing.T) {
	t.Parallel()
	if err := TranslateTaskItemkitError(nil); err != nil {
		t.Errorf("nil should pass through: got %v", err)
	}
}

func TestTranslateTaskItemkitErrorInvariant(t *testing.T) {
	t.Parallel()
	got := TranslateTaskItemkitError(errors.New("itemkit invariant: scheduled link count too high"))
	if extractCode(t, got) != apierrors.ItemItemkitInvariantViolation.Code {
		t.Errorf("task invariant: got %v", extractCode(t, got))
	}
}

func TestTranslateTaskItemkitErrorOther(t *testing.T) {
	t.Parallel()
	got := TranslateTaskItemkitError(errors.New("recurrence not allowed"))
	if extractCode(t, got) != apierrors.InternalUnexpected.Code {
		t.Errorf("task other: got %v", extractCode(t, got))
	}
}
