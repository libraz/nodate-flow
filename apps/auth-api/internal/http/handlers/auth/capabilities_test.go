package auth

import (
	"context"
	"testing"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

func TestCapabilities_AllEnabled(t *testing.T) {
	t.Parallel()
	deps := Deps{
		OIDC:                 &internauth.GoogleOIDCClient{},
		OIDCGithub:           &internauth.GithubOAuthClient{},
		OIDCMicrosoft:        &internauth.MicrosoftOIDCClient{},
		EmailSender:          &email.MemorySender{},
		Cipher:               &crypto.Cipher{},
		RegistrationOpen:     true,
		PasswordLoginEnabled: true,
	}
	handler := Capabilities(deps)
	out, err := handler(context.Background(), &struct{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := out.Body
	if !b.PasswordLogin {
		t.Error("passwordLogin should be true when enabled")
	}
	if !b.OIDCGoogle {
		t.Error("oidcGoogle should be true")
	}
	if !b.OIDCGithub {
		t.Error("oidcGithub should be true")
	}
	if !b.OIDCMicrosoft {
		t.Error("oidcMicrosoft should be true")
	}
	if !b.MagicLink {
		t.Error("magicLink should be true with real sender")
	}
	if !b.Totp {
		t.Error("totp should be true")
	}
	if !b.RegistrationOpen {
		t.Error("registrationOpen should be true")
	}
	if out.CacheControl != "public, max-age=3600" {
		t.Errorf("unexpected Cache-Control: %q", out.CacheControl)
	}
}

func TestCapabilities_AllDisabled(t *testing.T) {
	t.Parallel()
	deps := Deps{
		EmailSender: email.NoopSender{},
	}
	handler := Capabilities(deps)
	out, err := handler(context.Background(), &struct{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := out.Body
	if b.PasswordLogin {
		t.Error("passwordLogin should be false when disabled")
	}
	if b.OIDCGoogle {
		t.Error("oidcGoogle should be false")
	}
	if b.OIDCGithub {
		t.Error("oidcGithub should be false")
	}
	if b.OIDCMicrosoft {
		t.Error("oidcMicrosoft should be false")
	}
	if b.MagicLink {
		t.Error("magicLink should be false with NoopSender")
	}
	if b.Totp {
		t.Error("totp should be false")
	}
	if b.RegistrationOpen {
		t.Error("registrationOpen should be false")
	}
}

func TestCapabilities_Mixed(t *testing.T) {
	t.Parallel()
	deps := Deps{
		OIDC:        &internauth.GoogleOIDCClient{},
		EmailSender: &email.MemorySender{},
	}
	handler := Capabilities(deps)
	out, err := handler(context.Background(), &struct{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := out.Body
	if !b.OIDCGoogle {
		t.Error("oidcGoogle should be true")
	}
	if b.OIDCGithub {
		t.Error("oidcGithub should be false")
	}
	if b.OIDCMicrosoft {
		t.Error("oidcMicrosoft should be false")
	}
	if !b.MagicLink {
		t.Error("magicLink should be true")
	}
	if b.Totp {
		t.Error("totp should be false")
	}
}

func TestCapabilities_Immutable(t *testing.T) {
	t.Parallel()
	deps := Deps{EmailSender: email.NoopSender{}}
	handler := Capabilities(deps)
	out1, _ := handler(context.Background(), &struct{}{})
	out2, _ := handler(context.Background(), &struct{}{})
	if out1 != out2 {
		t.Error("handler should return the same pointer on every call")
	}
}

func TestCapabilities_NilSenderMeansNoMagicLink(t *testing.T) {
	t.Parallel()
	out, err := Capabilities(Deps{PasswordLoginEnabled: true})(context.Background(), &struct{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body.MagicLink {
		t.Error("magicLink should be false without a sender")
	}
	if !out.Body.PasswordLogin {
		t.Error("passwordLogin should follow PasswordLoginEnabled")
	}
}

func TestCapabilities_TotpNeedsPasswordLogin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		cipher *crypto.Cipher
		pw     bool
		want   bool
	}{
		{"cipher and password login", &crypto.Cipher{}, true, true},
		{"cipher without password login", &crypto.Cipher{}, false, false},
		{"password login without cipher", nil, true, false},
	} {
		out, err := Capabilities(Deps{Cipher: tc.cipher, PasswordLoginEnabled: tc.pw})(context.Background(), &struct{}{})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if out.Body.Totp != tc.want {
			t.Errorf("%s: totp = %v, want %v", tc.name, out.Body.Totp, tc.want)
		}
	}
}
