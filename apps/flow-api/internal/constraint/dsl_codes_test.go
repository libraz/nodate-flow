package constraint

import (
	"errors"
	"testing"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

func codeOf(t *testing.T, raw string) string {
	t.Helper()
	_, err := Parse([]byte(raw))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrParse) {
		t.Fatalf("errors.Is(ErrParse) false: %v", err)
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As(*ParseError) false: %v", err)
	}
	return pe.Code
}

func TestParseError_Codes(t *testing.T) {
	cases := map[string]string{
		`not json`:                         CodeInvalidJSON,
		`{"op":"bogus"}`:                   CodeUnsupportedOperator,
		`{"op":"and","terms":[]}`:          CodeEmptyTerms,
		`{"op":"time.due_before"}`:         CodeMissingArg,
		`{"op":"dependency.all_done"}`:     CodeMissingArg,
		`{"op":"dependency.open_at_most"}`: CodeMissingArg,
		`{"op":"actor.has_role"}`:          CodeMissingArg,
	}
	for in, want := range cases {
		if got := codeOf(t, in); got != want {
			t.Errorf("Parse(%q) code = %q, want %q", in, got, want)
		}
	}
}

func TestParseErrorCodesMatchGeneratedCatalog(t *testing.T) {
	cases := map[string]string{
		"invalid json":         apierrors.ConstraintParseInvalidJson.Code,
		"unsupported operator": apierrors.ConstraintParseUnsupportedOperator.Code,
		"missing arg":          apierrors.ConstraintParseMissingArg.Code,
		"empty terms":          apierrors.ConstraintParseEmptyTerms.Code,
	}
	local := map[string]string{
		"invalid json":         CodeInvalidJSON,
		"unsupported operator": CodeUnsupportedOperator,
		"missing arg":          CodeMissingArg,
		"empty terms":          CodeEmptyTerms,
	}
	for name, want := range cases {
		if got := local[name]; got != want {
			t.Fatalf("%s code drifted: local=%q generated=%q", name, got, want)
		}
	}
}
