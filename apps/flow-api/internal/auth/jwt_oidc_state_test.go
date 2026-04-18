package auth

import (
	"testing"
	"time"
)

func TestOIDCStateSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	iss, err := NewJWTIssuer(nil, "nodate-flow", "api", time.Minute)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}

	nonce := "abc123"
	tok, err := iss.SignOIDCState(nonce)
	if err != nil {
		t.Fatalf("sign oidc state: %v", err)
	}
	if tok == "" {
		t.Fatal("signed token is empty")
	}

	got, err := iss.VerifyOIDCState(tok)
	if err != nil {
		t.Fatalf("verify oidc state: %v", err)
	}
	if got != nonce {
		t.Fatalf("nonce mismatch: got %q want %q", got, nonce)
	}
}

func TestOIDCStateRejectsTampered(t *testing.T) {
	t.Parallel()
	iss, _ := NewJWTIssuer(nil, "nodate-flow", "api", time.Minute)
	tok, _ := iss.SignOIDCState("nonce123")
	if _, err := iss.VerifyOIDCState(tok + "garbage"); err == nil {
		t.Fatal("expected error on tampered oidc state token")
	}
}

func TestOIDCStateRejectsAccessToken(t *testing.T) {
	t.Parallel()
	iss, _ := NewJWTIssuer(nil, "nodate-flow", "api", time.Minute)
	// An access token should not be accepted as an OIDC state token
	// because they have different audiences.
	accessTok, _, _ := iss.Sign([16]byte{1}, [16]byte{2})
	if _, err := iss.VerifyOIDCState(accessTok); err == nil {
		t.Fatal("access token should not be accepted as oidc state")
	}
}

func TestOIDCStateRejectsTotpChallenge(t *testing.T) {
	t.Parallel()
	iss, _ := NewJWTIssuer(nil, "nodate-flow", "api", time.Minute)
	// A TOTP challenge token should not be accepted as an OIDC state token.
	totpTok, _, _ := iss.SignTotpChallenge(42)
	if _, err := iss.VerifyOIDCState(totpTok); err == nil {
		t.Fatal("totp challenge should not be accepted as oidc state")
	}
}
