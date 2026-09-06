package openapiutil

import (
	"reflect"
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

// componentsAPI returns a sub-API whose components are whatever mutate
// writes into them. Sections other than schemas have no registry to go
// through, so the case under test is stated by writing the section
// directly.
func componentsAPI(t *testing.T, mutate func(*huma.Components)) huma.API {
	t.Helper()
	_, api := humatest.New(t, huma.DefaultConfig("merge-test", "1.0.0"))
	mutate(api.OpenAPI().Components)
	return api
}

func bearerScheme() *huma.SecurityScheme {
	return &huma.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
}

// TestMergeAPIsKeepsNonSchemaComponentsFromLaterAPIs covers the whole
// point of merging components rather than only schemas: a group is free
// to declare a security scheme, a reusable response or a reusable
// parameter that the first group does not, and the merged document is
// the only one any client is generated from. A fold that copied schemas
// alone emitted operations referring to components that were not in the
// document — a spec that reads as complete and resolves to nothing.
func TestMergeAPIsKeepsNonSchemaComponentsFromLaterAPIs(t *testing.T) {
	t.Parallel()

	first := componentsAPI(t, func(c *huma.Components) {
		c.SecuritySchemes = map[string]*huma.SecurityScheme{"bearer": bearerScheme()}
	})
	second := componentsAPI(t, func(c *huma.Components) {
		c.SecuritySchemes = map[string]*huma.SecurityScheme{
			"apiToken": {Type: "apiKey", Name: "X-API-Token", In: "header"},
		}
		c.Responses = map[string]*huma.Response{
			"RateLimited": {Description: "Too many requests."},
		}
		c.Parameters = map[string]*huma.Param{
			"cursor": {Name: "cursor", In: "query", Schema: stringSchema()},
		}
	})

	merged, err := MergeAPIs([]huma.API{first, second})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	comps := merged.Components

	if comps.SecuritySchemes["apiToken"] == nil {
		t.Error("components.securitySchemes lost apiToken, declared only by the second sub-API")
	}
	if comps.SecuritySchemes["bearer"] == nil {
		t.Error("components.securitySchemes lost bearer, declared by the first sub-API")
	}
	if comps.Responses["RateLimited"] == nil {
		t.Error("components.responses lost RateLimited, declared only by the second sub-API")
	}
	if comps.Parameters["cursor"] == nil {
		t.Error("components.parameters lost cursor, declared only by the second sub-API")
	}
}

// TestMergeAPIsKeepsEveryComponentSectionFromLaterAPIs states the same
// requirement over the whole huma.Components struct instead of over the
// three sections a reader thinks of first. The named test above says what
// the loss looked like; this one is what keeps the list from going stale,
// because a huma release that adds a components section would otherwise
// reintroduce exactly the same silent loss with every existing test still
// green.
func TestMergeAPIsKeepsEveryComponentSectionFromLaterAPIs(t *testing.T) {
	t.Parallel()

	const key = "OnlyInSecond"

	second := componentsAPI(t, func(c *huma.Components) {
		v := reflect.ValueOf(c).Elem()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.Kind() != reflect.Map || field.Type().Key().Kind() != reflect.String {
				continue
			}
			if field.IsNil() {
				field.Set(reflect.MakeMap(field.Type()))
			}
			field.SetMapIndex(reflect.ValueOf(key), sampleEntry(field.Type().Elem()))
		}
	})

	merged, err := MergeAPIs([]huma.API{schemaAPI(t, nil), second})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := reflect.ValueOf(merged.Components).Elem()
	structType := got.Type()
	checked := 0
	for i := 0; i < structType.NumField(); i++ {
		field := got.Field(i)
		if field.Kind() != reflect.Map || field.Type().Key().Kind() != reflect.String {
			continue
		}
		checked++
		if !field.MapIndex(reflect.ValueOf(key)).IsValid() {
			t.Errorf("components.%s lost %q, declared only by the second sub-API",
				sectionName(structType.Field(i)), key)
		}
	}
	if checked == 0 {
		t.Fatal("no name-keyed components section was inspected, so nothing was proven")
	}
}

// sampleEntry returns a usable zero value for a components section's
// entry type, so the sweep above can populate a section without knowing
// which one it is.
func sampleEntry(elem reflect.Type) reflect.Value {
	switch elem.Kind() {
	case reflect.Pointer:
		return reflect.New(elem.Elem())
	case reflect.Interface:
		// The extensions section is map[string]any; any non-nil value
		// carries the name through the fold.
		return reflect.ValueOf("only-in-second")
	default:
		return reflect.New(elem).Elem()
	}
}

// TestMergeAPIsResolvesANonSchemaNameCollisionByFullness pins the rule
// the merge applies when two sub-APIs declare one component name, for the
// sections that are not schemas: the definition that structurally
// contains the other wins, whichever side it came in on. Keeping the
// first instead would pin the published contract to the order the router
// happens to register its groups in.
func TestMergeAPIsResolvesANonSchemaNameCollisionByFullness(t *testing.T) {
	t.Parallel()

	thin := &huma.SecurityScheme{Type: "http", Scheme: "bearer"}
	fuller := &huma.SecurityScheme{
		Type:        "http",
		Scheme:      "bearer",
		Description: "Workspace access token.",
	}
	withScheme := func(s *huma.SecurityScheme) func(*huma.Components) {
		return func(c *huma.Components) {
			c.SecuritySchemes = map[string]*huma.SecurityScheme{"bearer": s}
		}
	}

	for _, tc := range []struct {
		name string
		apis []huma.API
	}{
		{"fuller first", []huma.API{
			componentsAPI(t, withScheme(fuller)),
			componentsAPI(t, withScheme(thin)),
		}},
		{"thinner first", []huma.API{
			componentsAPI(t, withScheme(thin)),
			componentsAPI(t, withScheme(fuller)),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			merged, err := MergeAPIs(tc.apis)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			got := merged.Components.SecuritySchemes["bearer"]
			if got == nil {
				t.Fatal("bearer missing from the merged security schemes")
			}
			if got.Description != fuller.Description {
				t.Errorf("merged bearer description = %q, want %q; the thinner definition won",
					got.Description, fuller.Description)
			}
		})
	}
}

// TestMergeAPIsRefusesAGenuineNonSchemaCollision is the other half of the
// same rule. Two sub-APIs describing different things under one component
// name have no merged document that is correct for both, so the fold
// stops and names the section and the entry rather than publishing one of
// them for both.
func TestMergeAPIsRefusesAGenuineNonSchemaCollision(t *testing.T) {
	t.Parallel()

	header := &huma.SecurityScheme{Type: "apiKey", Name: "X-API-Token", In: "header"}
	query := &huma.SecurityScheme{Type: "apiKey", Name: "X-API-Token", In: "query"}
	withScheme := func(s *huma.SecurityScheme) func(*huma.Components) {
		return func(c *huma.Components) {
			c.SecuritySchemes = map[string]*huma.SecurityScheme{"apiToken": s}
		}
	}

	for _, tc := range []struct {
		name string
		apis []huma.API
	}{
		{"header first", []huma.API{
			componentsAPI(t, withScheme(header)),
			componentsAPI(t, withScheme(query)),
		}},
		{"query first", []huma.API{
			componentsAPI(t, withScheme(query)),
			componentsAPI(t, withScheme(header)),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := MergeAPIs(tc.apis)
			if err == nil {
				t.Fatal("merge accepted two different security schemes under one name")
			}
			if !strings.Contains(err.Error(), "securitySchemes") || !strings.Contains(err.Error(), "apiToken") {
				t.Errorf("error names neither the section nor the entry: %v", err)
			}
		})
	}
}

// TestMergeAPIsLeavesSubAPIComponentsAlone extends the fresh-document
// property to the sections other than schemas. The merged document's
// component maps have to be the fold's own: writing a later group's
// security scheme into the first group's map would hand that group a
// scheme it never declared, and the live server keeps serving from that
// same map for the rest of the process.
func TestMergeAPIsLeavesSubAPIComponentsAlone(t *testing.T) {
	t.Parallel()

	first := componentsAPI(t, func(c *huma.Components) {
		c.SecuritySchemes = map[string]*huma.SecurityScheme{"bearer": bearerScheme()}
	})
	second := componentsAPI(t, func(c *huma.Components) {
		c.SecuritySchemes = map[string]*huma.SecurityScheme{
			"apiToken": {Type: "apiKey", Name: "X-API-Token", In: "header"},
		}
	})
	apis := []huma.API{first, second}

	if _, err := MergeAPIs(apis); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if _, leaked := first.OpenAPI().Components.SecuritySchemes["apiToken"]; leaked {
		t.Error("the fold wrote another sub-API's security scheme into apis[0]'s components")
	}
	if _, err := MergeAPIs(apis); err != nil {
		t.Fatalf("second merge over the same APIs: %v", err)
	}
}

// docAPI returns a sub-API whose document-level fields are whatever
// mutate writes into them.
func docAPI(t *testing.T, mutate func(*huma.OpenAPI)) huma.API {
	t.Helper()
	_, api := humatest.New(t, huma.DefaultConfig("merge-test", "1.0.0"))
	mutate(api.OpenAPI())
	return api
}

func server(url string) *huma.Server { return &huma.Server{URL: url} }

func tag(name string) *huma.Tag { return &huma.Tag{Name: name} }

// TestMergeAPIsRefusesDisagreeingDocumentFields covers servers and
// security, the two document-wide fields no fold can combine. Keeping
// either group's silently would apply that group's setting to the other
// group's paths — declaring an auth requirement, or a base URL, for
// operations that never asked for one.
func TestMergeAPIsRefusesDisagreeingDocumentFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		first func(*huma.OpenAPI)
		later func(*huma.OpenAPI)
		field string
		wants []string
	}{
		{
			name:  "servers",
			first: func(o *huma.OpenAPI) { o.Servers = []*huma.Server{server("https://a.example")} },
			later: func(o *huma.OpenAPI) { o.Servers = []*huma.Server{server("https://b.example")} },
			field: "servers",
			wants: []string{"https://a.example", "https://b.example"},
		},
		{
			name:  "security",
			first: func(o *huma.OpenAPI) { o.Security = []map[string][]string{{"bearer": {}}} },
			later: func(o *huma.OpenAPI) { o.Security = []map[string][]string{{"apiToken": {}}} },
			field: "security",
			wants: []string{"bearer", "apiToken"},
		},
		{
			name:  "openapi version",
			first: func(o *huma.OpenAPI) { o.OpenAPI = "3.1.0" },
			later: func(o *huma.OpenAPI) { o.OpenAPI = "3.0.3" },
			field: "OpenAPI versions",
			wants: []string{"3.1.0", "3.0.3"},
		},
		{
			name:  "externalDocs",
			first: func(o *huma.OpenAPI) { o.ExternalDocs = &huma.ExternalDocs{URL: "https://a.example/docs"} },
			later: func(o *huma.OpenAPI) { o.ExternalDocs = &huma.ExternalDocs{URL: "https://b.example/docs"} },
			field: "externalDocs",
			wants: []string{"https://a.example/docs", "https://b.example/docs"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := MergeAPIs([]huma.API{docAPI(t, tc.first), docAPI(t, tc.later)})
			if err == nil {
				t.Fatalf("merge accepted two sub-APIs disagreeing about %s", tc.field)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error does not name the field %q: %v", tc.field, err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not carry the value %q: %v", want, err)
				}
			}
		})
	}
}

// TestMergeAPIsKeepsDocumentFieldsDeclaredOnlyByALaterAPI is the other
// side of the same rule: a field only one group declares is not in
// conflict with anything, so it belongs in the merged document rather
// than being dropped for having arrived late.
func TestMergeAPIsKeepsDocumentFieldsDeclaredOnlyByALaterAPI(t *testing.T) {
	t.Parallel()

	first := docAPI(t, func(*huma.OpenAPI) {})
	second := docAPI(t, func(o *huma.OpenAPI) {
		o.Servers = []*huma.Server{server("https://api.example")}
		o.Security = []map[string][]string{{"bearer": {}}}
	})

	merged, err := MergeAPIs([]huma.API{first, second})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(merged.Servers) != 1 || merged.Servers[0].URL != "https://api.example" {
		t.Errorf("servers = %+v, want the one the second sub-API declared", merged.Servers)
	}
	if len(merged.Security) != 1 {
		t.Errorf("security = %+v, want the one the second sub-API declared", merged.Security)
	}
}

// TestMergeAPIsAgreeingDocumentFieldsAreNotAConflict guards the refusal
// against over-reach. Every group of one service is built from one
// huma.Config, so they carry identical copies of these fields on every
// real merge; a check that read equal copies as a disagreement would
// stop the build for every service that declares a server at all.
func TestMergeAPIsAgreeingDocumentFieldsAreNotAConflict(t *testing.T) {
	t.Parallel()

	same := func(o *huma.OpenAPI) {
		o.Servers = []*huma.Server{server("https://api.example")}
		o.Security = []map[string][]string{{"bearer": {}}}
		o.ExternalDocs = &huma.ExternalDocs{URL: "https://api.example/docs"}
	}

	merged, err := MergeAPIs([]huma.API{docAPI(t, same), docAPI(t, same)})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ExternalDocs == nil || merged.ExternalDocs.URL != "https://api.example/docs" {
		t.Errorf("externalDocs = %+v, want the value both sub-APIs declared", merged.ExternalDocs)
	}
}

// TestMergeAPIsUnionsTagsInDeclarationOrder pins both halves of the tag
// rule: every group's tags reach the merged document, and the order they
// reach it in is fixed. The order is what makes the emitted document
// reproducible — a tag array assembled in Go map order would differ
// between two runs of the same generator over unchanged sources, and the
// only thing reading that difference is a drift check with nothing to
// report.
func TestMergeAPIsUnionsTagsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	apis := []huma.API{
		docAPI(t, func(o *huma.OpenAPI) { o.Tags = []*huma.Tag{tag("tasks"), tag("workspaces")} }),
		docAPI(t, func(o *huma.OpenAPI) { o.Tags = []*huma.Tag{tag("workspaces"), tag("calendars")} }),
		docAPI(t, func(o *huma.OpenAPI) { o.Tags = []*huma.Tag{tag("auth"), tag("tasks")} }),
	}
	want := []string{"tasks", "workspaces", "calendars", "auth"}

	for run := range 2 {
		merged, err := MergeAPIs(apis)
		if err != nil {
			t.Fatalf("merge (run %d): %v", run, err)
		}
		got := make([]string, 0, len(merged.Tags))
		for _, tg := range merged.Tags {
			got = append(got, tg.Name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: merged tags = %v, want %v", run, got, want)
		}
	}
}

// TestMergeAPIsLeavesSubAPITagsAlone covers the aliasing hazard the tag
// union introduces. apis[0]'s tag slice can have spare capacity, and
// appending to a shared slice writes into the backing array the sub-API
// is still serving from — invisible in its length, visible to anything
// that later grows the same slice. So the merged document gets its own
// array.
func TestMergeAPIsLeavesSubAPITagsAlone(t *testing.T) {
	t.Parallel()

	tags := make([]*huma.Tag, 1, 4)
	tags[0] = tag("tasks")
	first := docAPI(t, func(o *huma.OpenAPI) { o.Tags = tags })
	second := docAPI(t, func(o *huma.OpenAPI) { o.Tags = []*huma.Tag{tag("calendars")} })

	if _, err := MergeAPIs([]huma.API{first, second}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	own := first.OpenAPI().Tags
	if len(own) != 1 || own[0].Name != "tasks" {
		t.Fatalf("apis[0] tags = %+v, want only its own", own)
	}
	backing := own[:cap(own)]
	if backing[1] != nil {
		t.Errorf("the fold appended into apis[0]'s tag array: %+v", backing[1])
	}
}

// TestMergeAPIsKeepsTheFirstDocumentInfo pins the one document-level
// field that is resolved by taking the first group's copy rather than by
// agreement. Title, version and description name the service; there is
// no combining operation over two of them that yields a title anyone
// declared, and the outer fold has to tolerate two services that
// genuinely differ, so refusing here would put the two layers at odds.
func TestMergeAPIsKeepsTheFirstDocumentInfo(t *testing.T) {
	t.Parallel()

	first := docAPI(t, func(o *huma.OpenAPI) { o.Info = &huma.Info{Title: "first", Version: "1.0.0"} })
	second := docAPI(t, func(o *huma.OpenAPI) { o.Info = &huma.Info{Title: "second", Version: "2.0.0"} })

	merged, err := MergeAPIs([]huma.API{first, second})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.Info == nil || merged.Info.Title != "first" || merged.Info.Version != "1.0.0" {
		t.Errorf("merged info = %+v, want the first sub-API's", merged.Info)
	}
}
