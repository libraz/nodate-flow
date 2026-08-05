package log

import (
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// RedactHandler wraps another slog.Handler and scrubs secret-looking
// values from record attrs before forwarding. This is a thin re-export
// of the shared implementation in packages/go-shared/logutil.
type RedactHandler = logutil.RedactHandler

// NewRedactHandler returns a RedactHandler wrapping inner.
var NewRedactHandler = logutil.NewRedactHandler
