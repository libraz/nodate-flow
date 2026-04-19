package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ACLDB is the minimal database surface for ACL queries.
type ACLDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}

// RequireInstanceAdmin checks the instance_admins table for the authenticated
// user and rejects the request with 403 if no active grant exists.
func RequireInstanceAdmin(db ACLDB) func(http.Handler) http.Handler {
	const q = `SELECT 1 FROM instance_admins WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := authn.ActorFromContext(r.Context())
			if !ok {
				writeError(w, apierrors.InstanceAdminRequired.Status,
					apierrors.InstanceAdminRequired.Code,
					apierrors.InstanceAdminRequired.Message)
				return
			}
			var one int
			err := db.QueryRowContext(r.Context(), q, userID).Scan(&one)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, apierrors.InstanceAdminRequired.Status,
						apierrors.InstanceAdminRequired.Code,
						apierrors.InstanceAdminRequired.Message)
					return
				}
				writeError(w, 500, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
