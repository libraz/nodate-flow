package asanaimport

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kindPhrases are the detail fragments each kind must carry.
var kindPhrases = map[string]string{
	KindEmpty:               "mapping is empty",
	KindRequired:            "is required",
	KindInvalidEmail:        "is not a valid ASCII email address",
	KindDuplicate:           "is listed twice",
	KindInvalidSourceUserID: "is not a source user id",
	KindNoContainers:        "must list at least one",
	KindTooLong:             "is longer than",
	KindImporterListed:      "the importer must not be listed",
	KindInvalidRole:         "is not one of",
}

// validate runs both stages the way the importer does.
func validate(raw string, dryRun bool) []MappingError {
	m, err := ParseMapping(strings.NewReader(raw))
	if err != nil {
		var me MappingError
		if errors.As(err, &me) {
			return []MappingError{me}
		}
		return []MappingError{{Kind: KindInvalidYAML, Detail: err.Error()}}
	}
	return m.Validate(dryRun)
}

func TestMappingCommonFixtures(t *testing.T) {
	for _, c := range loadMappingFixture(t).Cases {
		t.Run(c.Name, func(t *testing.T) {
			var got []MappingError
			if c.Raw != nil {
				got = validate(*c.Raw, c.DryRun)
			} else {
				m := c.Mapping
				if m == nil {
					m = &Mapping{}
				}
				m.Workspace = "acme"
				m.Projects = nil
				if c.Container != nil {
					m.Projects = []MappingProject{*c.Container}
				}
				got = m.Validate(c.DryRun)
			}

			type key struct{ path, kind string }
			want := map[key]bool{}
			for _, e := range c.Expect {
				path := e.Path
				if path == "containers" || strings.HasPrefix(path, "containers[") {
					path = "projects" + strings.TrimPrefix(path, "containers")
				}
				want[key{path, e.Kind}] = true
			}
			have := map[key]bool{}
			for _, e := range got {
				k := key{e.Path, e.Kind}
				assert.False(t, have[k], "reported twice: %v", e)
				have[k] = true
				if e.Kind == KindUnknownKey {
					assert.Contains(t, e.Detail, "authors")
				} else if p, ok := kindPhrases[e.Kind]; ok {
					assert.Contains(t, e.Detail, p, "detail of %v", k)
				}
			}
			assert.Equal(t, want, have)
		})
	}
}

const flowMapping = `
workspace: acme
source_workspace: 1200000000
importer: ops@example.com
users:
  - { email: hanako@example.com, name: Hanako Yamada }
source_users:
  1201234567890: hanako@example.com
projects:
  - source: 1209876543210
    name: Marketing
    slug: marketing
    identifier: mkt
    members:
      - { email: hanako@example.com, role: lead }
    sections:
      "Untitled section": ""
      "Doing": "In progress"
`

func TestMappingFlowRules(t *testing.T) {
	t.Run("documented shape parses and validates", func(t *testing.T) {
		m, err := ParseMapping(strings.NewReader(flowMapping))
		require.NoError(t, err)
		assert.Equal(t, "acme", m.Workspace)
		assert.Equal(t, "1200000000", m.SourceWorkspace, "an integer reads as text")
		assert.Equal(t, map[string]string{"1201234567890": "hanako@example.com"}, m.SourceUsers)
		require.Len(t, m.Projects, 1)
		assert.Equal(t, "1209876543210", m.Projects[0].Source)
		assert.Equal(t, "", m.Projects[0].Sections["Untitled section"])
		assert.Empty(t, m.Validate(false))
	})

	t.Run("integer keys beyond 2^53 survive", func(t *testing.T) {
		m, err := ParseMapping(strings.NewReader("importer: a@example.com\nprojects:\n  - source: 9007199254740993123\n"))
		require.NoError(t, err)
		assert.Equal(t, "9007199254740993123", m.Projects[0].Source)
	})

	t.Run("unknown keys at every level are refused", func(t *testing.T) {
		for name, src := range map[string]string{
			"top":     "workspace: a\nauthors: {}\n",
			"project": "workspace: a\nprojects:\n  - {source: x, timetree: y}\n",
			"member":  "workspace: a\nprojects:\n  - {source: x, members: [{email: a@b.c, role: lead, color: red}]}\n",
			"user":    "workspace: a\nusers:\n  - {email: a@b.c, nane: x}\n",
		} {
			_, err := ParseMapping(strings.NewReader(src))
			var me MappingError
			require.ErrorAs(t, err, &me, name)
			assert.Equal(t, KindUnknownKey, me.Kind, name)
		}
	})

	t.Run("duplicate sections key is a YAML error", func(t *testing.T) {
		src := "workspace: a\nprojects:\n  - source: x\n    sections:\n      A: b\n      A: c\n"
		_, err := ParseMapping(strings.NewReader(src))
		require.Error(t, err)
	})

	t.Run("empty input", func(t *testing.T) {
		for _, src := range []string{"", "  \n", "# only a comment\n"} {
			errs := validate(src, false)
			require.Len(t, errs, 1)
			assert.Equal(t, KindEmpty, errs[0].Kind)
		}
		_, err := ParseMapping(nil)
		assert.Error(t, err)
	})

	base := func() *Mapping {
		m, err := ParseMapping(strings.NewReader(flowMapping))
		require.NoError(t, err)
		return m
	}
	// one applies mut to a valid mapping and returns the single error it causes.
	one := func(t *testing.T, mut func(*Mapping)) MappingError {
		t.Helper()
		m := base()
		mut(m)
		errs := m.Validate(false)
		require.Len(t, errs, 1, "%v", errs)
		return errs[0]
	}
	long := func(n int) string { return strings.Repeat("あ", n) }

	t.Run("workspace is required", func(t *testing.T) {
		e := one(t, func(m *Mapping) { m.Workspace = "" })
		assert.Equal(t, MappingError{"workspace", KindRequired, "is required"}, e)
	})

	t.Run("slug", func(t *testing.T) {
		for slug, ok := range map[string]bool{
			"a": true, "marketing-2": true, "0": true, strings.Repeat("a", 63): true,
			strings.Repeat("a", 64): false, "-a": false, "a-": false, "A": false,
			"a_b": false, "あ": false, "a b": false,
		} {
			errs := func() []MappingError {
				m := base()
				m.Projects[0].Slug = slug
				return m.Validate(false)
			}()
			if ok {
				assert.Empty(t, errs, slug)
				continue
			}
			require.Len(t, errs, 1, slug)
			assert.Equal(t, "projects[0].slug", errs[0].Path)
			assert.Equal(t, `"`+slug+`" is not a valid slug`, errs[0].Detail)
		}
		m := base()
		m.Projects[0].Slug = ""
		assert.Empty(t, m.Validate(false), "an empty slug takes the default")
	})

	t.Run("identifier", func(t *testing.T) {
		for id, ok := range map[string]bool{
			"": true, "A": true, "mkt": true, "A1B2C": true, "ABCDEF": false, "A-B": false, "あ": false, "A B": false,
		} {
			m := base()
			m.Projects[0].Identifier = id
			errs := m.Validate(false)
			if ok {
				assert.Empty(t, errs, id)
				continue
			}
			require.Len(t, errs, 1, id)
			assert.Equal(t, "projects[0].identifier", errs[0].Path)
			assert.Equal(t, `"`+id+`" is not a valid identifier`, errs[0].Detail)
		}
	})

	t.Run("slug and identifier duplicates", func(t *testing.T) {
		m := base()
		m.Projects = append(m.Projects, MappingProject{Source: "2", Slug: "marketing", Identifier: "MKT"})
		errs := m.Validate(false)
		require.Len(t, errs, 2)
		assert.Equal(t, MappingError{"projects[1].slug", KindDuplicate, `"marketing" is listed twice`}, errs[0])
		assert.Equal(t, MappingError{"projects[1].identifier", KindDuplicate, `"MKT" is listed twice`}, errs[1], "identifiers compare upper-cased")
	})

	t.Run("role", func(t *testing.T) {
		for _, role := range projectRoles {
			m := base()
			m.Projects[0].Members[0].Role = role
			assert.Empty(t, m.Validate(false), role)
		}
		e := one(t, func(m *Mapping) { m.Projects[0].Members[0].Role = "owner" })
		assert.Equal(t, MappingError{
			"projects[0].members[0].role", KindInvalidRole,
			`"owner" is not one of lead, editor, commenter, viewer`,
		}, e)
	})

	t.Run("section label length counts runes", func(t *testing.T) {
		m := base()
		m.Projects[0].Sections["Doing"] = long(64)
		assert.Empty(t, m.Validate(false))
		e := one(t, func(m *Mapping) { m.Projects[0].Sections["Doing"] = long(65) })
		assert.Equal(t, MappingError{`projects[0].sections["Doing"]`, KindTooLong, "is longer than 64 characters"}, e)
	})

	t.Run("user name length counts runes", func(t *testing.T) {
		m := base()
		m.Users[0].Name = long(255)
		assert.Empty(t, m.Validate(false))
		e := one(t, func(m *Mapping) { m.Users[0].Name = long(256) })
		assert.Equal(t, MappingError{"users[0].name", KindTooLong, "is longer than 255 characters"}, e)
	})

	t.Run("projects may be empty only in a dry run", func(t *testing.T) {
		m := base()
		m.Projects = nil
		assert.Empty(t, m.Validate(true))
		errs := m.Validate(false)
		require.Len(t, errs, 1)
		assert.Equal(t, MappingError{"projects", KindNoContainers, "must list at least one project"}, errs[0])
	})

	t.Run("error text", func(t *testing.T) {
		assert.Equal(t, `importer: is required`, MappingError{"importer", KindRequired, "is required"}.Error())
		assert.Equal(t, `mapping is empty`, MappingError{"", KindEmpty, "mapping is empty"}.Error())
	})
}
