package auth

import (
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr
