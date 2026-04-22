package auth

import (
	"context"
	"testing"

	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

func TestCapabilities_AllEnabled(t *testing.T) {
	t.Parallel()
	deps := Deps{
		OIDC:             &internauth.OIDCClient{},
		OIDCGithub:       &internauth.GithubOAuthClient{},
		OIDCMicrosoft:    &internauth.MicrosoftOIDCClient{},
		EmailSender:      &email.MemorySender{},
		Cipher:           &crypto.Cipher{},
		RegistrationOpen: true,
	}
	handler := Capabilities(deps)
	out, err := handler(context.Background(), &struct{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := out.Body
	if !b.PasswordLogin {
		t.Error("passwordLogin should always be true")
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
	if !b.PasswordLogin {
		t.Error("passwordLogin should always be true")
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
		OIDC:        &internauth.OIDCClient{},
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
