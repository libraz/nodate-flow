// Package handlerutil — itemkit error translation.
//
// itemkit returns plain Go errors with semantic substrings (the
// "itemkit invariant" / "recurrence" / "not found" sentinels are
// matched by message). Different domains want to map those into
// different apierrors specs (a calendar event 404 vs. a task 404),
// so the translator splits into per-domain functions sharing a
// single classifier.
package handlerutil

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// ItemkitErrorKind is the classifier output. It is intentionally
// narrow — every domain-specific translator decides how to map each
// kind to its own apierrors.Spec set. New invariant categories should
// be added here first, then handled in every translator.
type ItemkitErrorKind int

const (
	// ItemkitErrorNone is the sentinel for "no error".
	ItemkitErrorNone ItemkitErrorKind = iota
	// ItemkitErrorNotFound covers sql.ErrNoRows plus messages
	// containing "not found".
	ItemkitErrorNotFound
	// ItemkitErrorRecurrenceWithLink covers the "recurrence" /
	// "recurring events cannot be linked" invariant.
	ItemkitErrorRecurrenceWithLink
	// ItemkitErrorInvariant covers the generic "itemkit invariant"
	// substring — typically a cross-table constraint violation.
	ItemkitErrorInvariant
	// ItemkitErrorOther is the catch-all for everything else.
	// Callers map this to a domain-specific 500.
	ItemkitErrorOther
)

// ClassifyItemkitError inspects an itemkit error and returns the
// classifier kind. nil → ItemkitErrorNone. The function does not
// log; logging is the caller's job so slog attributes (component,
// op) reflect the domain.
func ClassifyItemkitError(err error) ItemkitErrorKind {
	if err == nil {
		return ItemkitErrorNone
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ItemkitErrorNotFound
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return ItemkitErrorNotFound
	case strings.Contains(msg, "recurrence"):
		return ItemkitErrorRecurrenceWithLink
	case strings.Contains(msg, "itemkit invariant"):
		return ItemkitErrorInvariant
	}
	return ItemkitErrorOther
}

// TranslateCalendarItemkitError maps an itemkit error coming from a
// calendar-domain handler into the calendar apierrors spec set. The
// op string is logged so a 500 does not disappear into a generic
// "store write failed" without a breadcrumb. fallback is the
// op-specific 5xx (write vs. delete) used for the generic case.
//
// nil err → nil error.
func TranslateCalendarItemkitError(ctx context.Context, op string, err error, fallback *apierrors.Spec) error {
	if err == nil {
		return nil
	}
	slog.ErrorContext(ctx, "itemkit error", "component", "calendar", "op", op, "error", err.Error())
	switch ClassifyItemkitError(err) {
	case ItemkitErrorNotFound:
		return HTTPErr(apierrors.CalendarEventNotFound)
	case ItemkitErrorRecurrenceWithLink:
		return HTTPErr(apierrors.ItemItemkitRecurrenceWithTaskLink)
	case ItemkitErrorInvariant:
		return HTTPErr(apierrors.ItemItemkitInvariantViolation)
	}
	if fallback != nil {
		return HTTPErr(fallback)
	}
	return HTTPErr(apierrors.CalendarEventStoreWriteInterrupted)
}

// TranslateTaskItemkitError maps an itemkit error coming from a
// task-domain handler into the task apierrors spec set. Every kind but
// Other answers with the same public code the calendar and MCP
// translators give it; a row disabled between the handler's resolve and
// itemkit's in-transaction re-read is a 404, not a server error. Only
// ItemkitErrorOther collapses into INTERNAL.UNEXPECTED. nil err → nil.
func TranslateTaskItemkitError(err error) error {
	switch ClassifyItemkitError(err) {
	case ItemkitErrorNone:
		return nil
	case ItemkitErrorNotFound:
		if itemkitMissingEvent(err) {
			return HTTPErr(apierrors.CalendarEventNotFound)
		}
		return HTTPErr(apierrors.WsTaskNotFound)
	case ItemkitErrorRecurrenceWithLink:
		return HTTPErr(apierrors.ItemItemkitRecurrenceWithTaskLink)
	case ItemkitErrorInvariant:
		return HTTPErr(apierrors.ItemItemkitInvariantViolation)
	}
	return HTTPErr(apierrors.InternalUnexpected)
}

// itemkitMissingEvent reports whether a not-found itemkit error names the
// event rather than the task; itemkit words both "itemkit: <kind> N not
// found". A task handler's own row is the default.
func itemkitMissingEvent(err error) bool {
	return strings.Contains(err.Error(), "itemkit: event ")
}
