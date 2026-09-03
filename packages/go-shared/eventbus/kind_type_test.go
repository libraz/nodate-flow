package eventbus

import (
	"reflect"
	"testing"
)

// TestKindIsDefinedType pins Kind as a defined type rather than an
// alias for string. As an alias every string literal in the tree is a
// valid event kind, which is how a kind nobody subscribes to gets
// written: it compiles, it inserts, and the only symptom is a
// notification that never arrives. As a defined type the compiler
// rejects a bare string, so minting a kind is a deliberate conversion.
//
// Restoring `type Kind = string` fails this test.
func TestKindIsDefinedType(t *testing.T) {
	t.Parallel()

	kindType := reflect.TypeOf(Kind(""))
	if kindType.Kind() != reflect.String {
		t.Fatalf("Kind underlying type = %v, want %v", kindType.Kind(), reflect.String)
	}
	if kindType == reflect.TypeOf("") {
		t.Fatal("Kind is an alias for string; a raw string would be accepted wherever an event kind is expected")
	}
	if kindType.Name() != "Kind" {
		t.Fatalf("Kind type name = %q, want %q", kindType.Name(), "Kind")
	}
}
