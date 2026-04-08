package auth

import (
	"context"

	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Me handles GET /me. It reads the actor user id injected by the auth
// middleware and returns the matching user profile.
func Me(deps Deps) func(context.Context, *struct{}) (*MeOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*MeOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		const q = `SELECT public_id, email, display_name, locale FROM users WHERE id = ? AND enabled = TRUE LIMIT 1`
		var pub [16]byte
		var email, name, locale string
		if err := deps.DB.QueryRowContext(ctx, q, uid).Scan(&pub, &email, &name, &locale); err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		out := &MeOutput{}
		out.Body.ID = uuidStr(pub)
		out.Body.Email = email
		out.Body.DisplayName = name
		out.Body.Locale = locale
		return out, nil
	}
}

func uuidStr(b [16]byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i, x := range b {
		out[j] = hex[x>>4]
		out[j+1] = hex[x&0x0f]
		j += 2
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out[j] = '-'
			j++
		}
	}
	return string(out)
}
