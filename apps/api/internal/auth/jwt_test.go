package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

func TestJWTSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	iss, err := NewJWTIssuer(nil, "nodate-flow", "api", time.Minute)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	sub := types.FromUUID(uuid.New())
	sid := types.FromUUID(uuid.New())
	tok, exp, err := iss.Sign(sub, sid)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if exp.Before(time.Now()) {
		t.Fatal("expiry in past")
	}
	claims, err := iss.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserPublicID != sub {
		t.Fatalf("sub mismatch: got %s want %s", claims.UserPublicID, sub)
	}
}

func TestJWTVerifyRejectsTampered(t *testing.T) {
	t.Parallel()
	iss, _ := NewJWTIssuer(nil, "nodate-flow", "api", time.Minute)
	tok, _, _ := iss.Sign(types.FromUUID(uuid.New()), types.FromUUID(uuid.New()))
	if _, err := iss.Verify(tok + "garbage"); err == nil {
		t.Fatal("expected error on tampered token")
	}
}
