package auth

import (
	"os"
	"strings"
	"testing"
)

func TestTotpEnrollAndConfirmRequirePasswordReauth_SourceGuard(t *testing.T) {
	dto, err := os.ReadFile("dto.go")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := os.ReadFile("totp.go")
	if err != nil {
		t.Fatal(err)
	}
	dtoSource := string(dto)
	handlerSource := string(handler)

	for _, want := range []string{
		"type TotpEnrollInput struct",
		"Password string `json:\"password\" minLength:\"1\" maxLength:\"256\"`",
		"Code     string `json:\"code\"",
	} {
		if !strings.Contains(dtoSource, want) {
			t.Fatalf("TOTP DTO must include password reauth contract: missing %q", want)
		}
	}

	for _, want := range []string{
		"func TotpEnroll(deps Deps) func(context.Context, *TotpEnrollInput)",
		"verifyLocalIdentityPassword(row, in.Body.Password)",
		"AuthPasswordNoLocalIdentity",
		"AuthPasswordCurrentMismatch",
	} {
		if !strings.Contains(handlerSource, want) {
			t.Fatalf("TOTP handlers must enforce password reauth: missing %q", want)
		}
	}
	if strings.Count(handlerSource, "verifyLocalIdentityPassword(row, in.Body.Password)") < 4 {
		t.Fatal("enroll, confirm, recovery regeneration, and disable must all use the shared password reauth helper")
	}
}
