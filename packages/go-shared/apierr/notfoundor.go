package apierr

import (
	"database/sql"
	"errors"
)

// NotFoundOr returns notFound when err matches sql.ErrNoRows, otherwise it
// returns internal. Both arguments are pre-built APIError values produced
// by apierr.New / apierr.Wrap; pass whatever the calling service uses for
// its "row not found" and "unexpected internal error" codes.
//
// This collapses the canonical handler pattern:
//
//	if err != nil {
//	    if errors.Is(err, sql.ErrNoRows) {
//	        return nil, httpErr(apierrors.XNotFound)
//	    }
//	    return nil, httpErr(apierrors.InternalUnexpected)
//	}
//
// into:
//
//	if err != nil {
//	    return nil, httpErr(apierr.NotFoundOr(err, notFoundSpec, internalSpec).Spec)
//	}
//
// or — when the call site already operates on *APIError — directly:
//
//	if err != nil {
//	    return apierr.NotFoundOr(err, errNotFound, errInternal)
//	}
//
// nil err is treated as "not no-rows" and returns internal; callers must
// continue to short-circuit the err == nil branch themselves.
func NotFoundOr(err error, notFound, internal *APIError) *APIError {
	if errors.Is(err, sql.ErrNoRows) {
		return notFound
	}
	return internal
}

// SpecForErrNoRows is the Spec-level companion to NotFoundOr. Most existing
// handlers thread *Spec values through a local httpErr alias, so this
// variant keeps the call shape identical while removing the boilerplate:
//
//	if err != nil {
//	    return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.XNotFound, apierrors.InternalUnexpected))
//	}
//
// Behaviour is otherwise identical to NotFoundOr.
func SpecForErrNoRows(err error, notFound, internal *Spec) *Spec {
	if errors.Is(err, sql.ErrNoRows) {
		return notFound
	}
	return internal
}
