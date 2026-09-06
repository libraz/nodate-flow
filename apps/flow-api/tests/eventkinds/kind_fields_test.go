package eventkinds

import (
	"reflect"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// modulePath prefixes every type of this module, and is how the check
// below tells the entries it can answer for from ones naming another
// module's types.
const modulePath = "github.com/libraz/nodate-flow/apps/flow-api"

// paramsWritingAnEventKind is every generated params struct in this
// module that carries an event kind into a row: the two that append to
// `events` and the one that writes a notification's event_type.
//
// The values are here to be compiled. kindscan names these types as
// strings — it cannot import them — so a query renamed in sqlc.yaml, or a
// column renamed in the schema, leaves the guard pointing at a type that
// no longer exists and reporting nothing, which is indistinguishable from
// a clean tree. This list is what the compiler and the test below have to
// say about that.
var paramsWritingAnEventKind = []any{
	generated.AppendEventParams{},
	generated.AppendAgentEventParams{},
	generated.CreateNotificationParams{},
}

// TestKindFieldsResolveInThisModule proves every field the origin rule
// covers here still exists, and still holds a string — the property that
// made the rule necessary in the first place.
func TestKindFieldsResolveInThisModule(t *testing.T) {
	t.Parallel()

	byName := map[string]reflect.Type{}
	for _, params := range paramsWritingAnEventKind {
		rt := reflect.TypeOf(params)
		byName[rt.PkgPath()+"."+rt.Name()] = rt
	}

	covered := map[string]bool{}
	for _, field := range kindscan.KindFields() {
		if !strings.HasPrefix(field.Type, modulePath) {
			continue
		}
		rt, ok := byName[field.Type]
		if !ok {
			t.Errorf("the origin rule names %s, which this module does not declare; "+
				"the rule covers nothing until the entry in packages/go-shared/kindscan is corrected", field.Type)
			continue
		}
		structField, ok := rt.FieldByName(field.Field)
		if !ok {
			t.Errorf("the origin rule names %s, but %s has no such field", field, field.Type)
			continue
		}
		if structField.Type.Kind() != reflect.String {
			t.Errorf("%s is %s, not a string; a kind reaching it is the type checker's to reject, "+
				"and the rule is covering a field that no longer needs it", field, structField.Type)
		}
		covered[field.Type] = true
	}

	for name := range byName {
		if !covered[name] {
			t.Errorf("%s carries an event kind into a row but the origin rule does not cover it; "+
				"add it to kindFields in packages/go-shared/kindscan", name)
		}
	}
}
