package router

import (
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
)

// schemaAPI returns a sub-API whose schema registry holds exactly the
// given named schemas. The merge consumes registries, not handlers, so
// writing the registry directly states the case under test without
// routing a request to produce it.
func schemaAPI(t *testing.T, schemas map[string]*huma.Schema) huma.API {
	t.Helper()
	_, api := humatest.New(t, huma.DefaultConfig("merge-test", "1.0.0"))
	reg := api.OpenAPI().Components.Schemas.Map()
	for name, s := range schemas {
		reg[name] = s
	}
	return api
}

func objectSchema(props map[string]*huma.Schema) *huma.Schema {
	return &huma.Schema{Type: "object", Properties: props}
}

func stringSchema() *huma.Schema { return &huma.Schema{Type: "string"} }

// TestMergeAPIsPrefersTheFullerRendering is the case that made the SDK
// wrong: one registry uses a type as an operation's direct body and gets
// huma's `$schema` self-description property, another only ever nests it
// and does not. Keeping whichever registry was folded first published the
// thinner rendering, and which one that was depended on the order the
// router happened to register its groups in.
func TestMergeAPIsPrefersTheFullerRendering(t *testing.T) {
	t.Parallel()

	thin := objectSchema(map[string]*huma.Schema{"id": stringSchema()})
	fuller := objectSchema(map[string]*huma.Schema{
		"id":      stringSchema(),
		"$schema": {Type: "string", Format: "uri", ReadOnly: true},
	})

	for _, tc := range []struct {
		name string
		apis []huma.API
	}{
		{"fuller first", []huma.API{
			schemaAPI(t, map[string]*huma.Schema{"Widget": fuller}),
			schemaAPI(t, map[string]*huma.Schema{"Widget": thin}),
		}},
		{"thinner first", []huma.API{
			schemaAPI(t, map[string]*huma.Schema{"Widget": thin}),
			schemaAPI(t, map[string]*huma.Schema{"Widget": fuller}),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			merged, err := MergeAPIs(tc.apis)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			got := merged.Components.Schemas.Map()["Widget"]
			if got == nil {
				t.Fatal("Widget missing from the merged registry")
			}
			if got.Properties["$schema"] == nil {
				t.Error("merged Widget lost the $schema property; the thinner rendering won")
			}
			if got.Properties["id"] == nil {
				t.Error("merged Widget lost the id property")
			}
		})
	}
}

// TestMergeAPIsRefusesAGenuineNameCollision pins the other half of the
// rule. When neither rendering contains the other, the two sub-APIs are
// describing two different types under one schema name; no document can
// be correct for both, so the merge stops and names the schema instead of
// picking one and generating an SDK that is wrong about half the calls.
func TestMergeAPIsRefusesAGenuineNameCollision(t *testing.T) {
	t.Parallel()

	byID := objectSchema(map[string]*huma.Schema{"id": stringSchema()})
	byName := objectSchema(map[string]*huma.Schema{"name": stringSchema()})

	for _, tc := range []struct {
		name string
		apis []huma.API
	}{
		{"id first", []huma.API{
			schemaAPI(t, map[string]*huma.Schema{"Widget": byID}),
			schemaAPI(t, map[string]*huma.Schema{"Widget": byName}),
		}},
		{"name first", []huma.API{
			schemaAPI(t, map[string]*huma.Schema{"Widget": byName}),
			schemaAPI(t, map[string]*huma.Schema{"Widget": byID}),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := MergeAPIs(tc.apis)
			if err == nil {
				t.Fatal("merge accepted two different schemas under one name")
			}
			if !strings.Contains(err.Error(), "Widget") {
				t.Errorf("error does not name the colliding schema: %v", err)
			}
		})
	}
}

// TestMergeAPIsLeavesSubAPIDocumentsAlone guards the property that lets
// the live route and cmd/dump-openapi fold the same slice: the fold
// builds a new document instead of growing apis[0]'s. When it grew
// apis[0]'s, the second fold compared the first fold's normalized
// ErrorModel against every other sub-API's untouched one and reported a
// collision that did not exist.
func TestMergeAPIsLeavesSubAPIDocumentsAlone(t *testing.T) {
	t.Parallel()

	first := schemaAPI(t, map[string]*huma.Schema{
		"OnlyInFirst": objectSchema(nil),
		"ErrorModel":  objectSchema(map[string]*huma.Schema{"type": {Type: "string", Format: "uri-reference"}}),
	})
	second := schemaAPI(t, map[string]*huma.Schema{
		"OnlyInSecond": objectSchema(nil),
		"ErrorModel":   objectSchema(map[string]*huma.Schema{"type": {Type: "string", Format: "uri-reference"}}),
	})
	apis := []huma.API{first, second}

	merged, err := MergeAPIs(apis)
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if merged.Components.Schemas.Map()["ErrorModel"].Properties["extensions"] == nil {
		t.Error("merged document was not normalized")
	}

	firstReg := first.OpenAPI().Components.Schemas.Map()
	if _, leaked := firstReg["OnlyInSecond"]; leaked {
		t.Error("the fold wrote another sub-API's schema into apis[0]'s registry")
	}
	if firstReg["ErrorModel"].Properties["extensions"] != nil {
		t.Error("normalizing the merged document rewrote apis[0]'s own ErrorModel")
	}

	if _, err := MergeAPIs(apis); err != nil {
		t.Fatalf("second merge over the same APIs: %v", err)
	}
}

// TestMergeAPIsUnionsVerbsWithoutTouchingTheSubAPI covers the shape the
// real router produces: a resource whose reads live in one chi group and
// whose writes live in another appears in both documents carrying only
// its own verbs. The merged document needs both, and the group documents
// must still carry only their own — the per-builder ACL checks read them
// back to decide which operations a builder is answerable for, and a
// group credited with the other group's write would be checked against
// the wrong floor.
func TestMergeAPIsUnionsVerbsWithoutTouchingTheSubAPI(t *testing.T) {
	t.Parallel()

	readOp := &huma.Operation{OperationID: "get-widget"}
	writeOp := &huma.Operation{OperationID: "delete-widget"}

	reader := schemaAPI(t, nil)
	reader.OpenAPI().Paths = map[string]*huma.PathItem{"/widgets/{id}": {Get: readOp}}
	writer := schemaAPI(t, nil)
	writer.OpenAPI().Paths = map[string]*huma.PathItem{"/widgets/{id}": {Delete: writeOp}}

	merged, err := MergeAPIs([]huma.API{reader, writer})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	item := merged.Paths["/widgets/{id}"]
	if item == nil || item.Get == nil || item.Delete == nil {
		t.Fatalf("merged path item lost a verb: %+v", item)
	}
	if reader.OpenAPI().Paths["/widgets/{id}"].Delete != nil {
		t.Error("the fold gave the reading group the writing group's DELETE")
	}
	if writer.OpenAPI().Paths["/widgets/{id}"].Get != nil {
		t.Error("the fold gave the writing group the reading group's GET")
	}
}
