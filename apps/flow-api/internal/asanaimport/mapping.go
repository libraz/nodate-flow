// Package asanaimport imports Asana projects into nodate-flow.
//
// This file holds the operator's mapping document: its types, parsing, and
// the stage-1 validation that needs neither Asana nor the database.
package asanaimport

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	maxNameRunes    = 255 // projects.name and users.name
	maxSectionRunes = 64  // labels.name
	maxSlugLen      = 63
	maxIdentifier   = 5
)

// Kinds of mapping problem. They match the kind column of the shared
// importer specification one to one.
const (
	KindEmpty               = "empty"
	KindUnknownKey          = "unknown_key"
	KindInvalidYAML         = "invalid_yaml"
	KindRequired            = "required"
	KindInvalidEmail        = "invalid_email"
	KindDuplicate           = "duplicate"
	KindInvalidSourceUserID = "invalid_source_user_id"
	KindNoContainers        = "no_containers"
	KindTooLong             = "too_long"
	KindImporterListed      = "importer_listed"
	KindInvalidRole         = "invalid_role"
	KindInvalidSlug         = "invalid_slug"
	KindInvalidIdentifier   = "invalid_identifier"
)

// projectRoles is the role vocabulary, in the order the error text lists it.
var projectRoles = []string{"lead", "editor", "commenter", "viewer"}

// MappingError is one problem found in the mapping. Path is the YAML
// position, empty when the problem concerns the whole document.
type MappingError struct {
	Path   string
	Kind   string
	Detail string
}

// Error renders "<path>: <detail>", or just the detail when Path is empty.
func (e MappingError) Error() string {
	if e.Path == "" {
		return e.Detail
	}
	return e.Path + ": " + e.Detail
}

// Mapping describes how Asana projects land in flow.
type Mapping struct {
	Workspace       string            `yaml:"workspace"`
	SourceWorkspace string            `yaml:"source_workspace"`
	Importer        string            `yaml:"importer"`
	Users           []MappingUser     `yaml:"users"`
	SourceUsers     map[string]string `yaml:"source_users"`
	Projects        []MappingProject  `yaml:"projects"`
}

// MappingUser is an account to create before anything else.
type MappingUser struct {
	Email string `yaml:"email"`
	Name  string `yaml:"name"`
}

// MappingProject maps one Asana project.
type MappingProject struct {
	Source     string          `yaml:"source"`
	Name       string          `yaml:"name"`
	Slug       string          `yaml:"slug"`
	Identifier string          `yaml:"identifier"`
	Members    []MappingMember `yaml:"members"`
	// Sections maps an Asana section name to a label name; "" means no label.
	Sections map[string]string `yaml:"sections"`
}

// MappingMember is a project member and the role it is given.
type MappingMember struct {
	Email string `yaml:"email"`
	Role  string `yaml:"role"`
}

// NormalizeEmail is the form addresses are compared in.
func NormalizeEmail(email string) string { return strings.ToLower(email) }

// ParseMapping decodes the YAML, refusing unknown keys and empty input. The
// returned error is a MappingError.
func ParseMapping(r io.Reader) (*Mapping, error) {
	if r == nil {
		return nil, MappingError{Kind: KindEmpty, Detail: "mapping is empty"}
	}
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var m Mapping
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, MappingError{Kind: KindEmpty, Detail: "mapping is empty"}
		}
		kind := KindInvalidYAML
		if strings.Contains(err.Error(), "not found in type") {
			kind = KindUnknownKey
		}
		return nil, MappingError{Kind: kind, Detail: err.Error()}
	}
	return &m, nil
}

// validEmail applies the sign-in allow-list rule: printable ASCII, local@domain.
func validEmail(email string) bool {
	for i := 0; i < len(email); i++ {
		if c := email[i]; c <= ' ' || c >= 0x7f {
			return false
		}
	}
	local, domain, ok := strings.Cut(email, "@")
	return ok && local != "" && domain != "" && !strings.Contains(domain, "@")
}

// isSourceUserID reports whether s is an Asana gid: decimal digits.
func isSourceUserID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isLowerAlnum(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }

// validSlug matches ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ within 63 characters.
func validSlug(s string) bool {
	if s == "" || len(s) > maxSlugLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isLowerAlnum(c) && (c != '-' || i == 0 || i == len(s)-1) {
			return false
		}
	}
	return true
}

// validIdentifier accepts 1 to 5 ASCII letters or digits.
func validIdentifier(s string) bool {
	if s == "" || len(s) > maxIdentifier {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum {
			return false
		}
	}
	return true
}

func validRole(role string) bool {
	for _, r := range projectRoles {
		if role == r {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Validate checks the mapping on its own and returns every problem found,
// in document order. A dry run may name no projects.
func (m *Mapping) Validate(dryRun bool) []MappingError {
	var errs []MappingError
	add := func(path, kind, format string, a ...any) {
		errs = append(errs, MappingError{Path: path, Kind: kind, Detail: fmt.Sprintf(format, a...)})
	}
	email := func(path, v string, seen map[string]bool) {
		switch {
		case v == "":
			add(path, KindRequired, "is required")
		case !validEmail(v):
			add(path, KindInvalidEmail, "%q is not a valid ASCII email address", v)
		case seen != nil && seen[NormalizeEmail(v)]:
			add(path, KindDuplicate, "%q is listed twice", v)
		}
		if seen != nil && v != "" {
			seen[NormalizeEmail(v)] = true
		}
	}

	if m.Workspace == "" {
		add("workspace", KindRequired, "is required")
	}
	email("importer", m.Importer, nil)
	importer := NormalizeEmail(m.Importer)

	seenUsers := map[string]bool{}
	for i, u := range m.Users {
		email(fmt.Sprintf("users[%d].email", i), u.Email, seenUsers)
		if utf8.RuneCountInString(u.Name) > maxNameRunes {
			add(fmt.Sprintf("users[%d].name", i), KindTooLong, "is longer than %d characters", maxNameRunes)
		}
	}

	for _, id := range sortedKeys(m.SourceUsers) {
		path := fmt.Sprintf("source_users[%q]", id)
		if !isSourceUserID(id) {
			add(path, KindInvalidSourceUserID, "is not a source user id")
		}
		if v := m.SourceUsers[id]; !validEmail(v) {
			add(path, KindInvalidEmail, "%q is not a valid ASCII email address", v)
		}
	}

	if len(m.Projects) == 0 && !dryRun {
		add("projects", KindNoContainers, "must list at least one project")
	}

	seenSource, seenName := map[string]bool{}, map[string]bool{}
	seenSlug, seenIdent := map[string]bool{}, map[string]bool{}
	for i, p := range m.Projects {
		where := fmt.Sprintf("projects[%d]", i)
		if p.Source == "" {
			add(where+".source", KindRequired, "is required")
		} else if seenSource[p.Source] {
			add(where+".source", KindDuplicate, "%q is listed twice", p.Source)
		}
		seenSource[p.Source] = true

		if p.Name != "" {
			if utf8.RuneCountInString(p.Name) > maxNameRunes {
				add(where+".name", KindTooLong, "is longer than %d characters", maxNameRunes)
			}
			if seenName[p.Name] {
				add(where+".name", KindDuplicate, "%q is listed twice", p.Name)
			}
			seenName[p.Name] = true
		}

		if p.Slug != "" {
			if !validSlug(p.Slug) {
				add(where+".slug", KindInvalidSlug, "%q is not a valid slug", p.Slug)
			} else if seenSlug[p.Slug] {
				add(where+".slug", KindDuplicate, "%q is listed twice", p.Slug)
			}
			seenSlug[p.Slug] = true
		}
		if p.Identifier != "" {
			up := strings.ToUpper(p.Identifier)
			if !validIdentifier(p.Identifier) {
				add(where+".identifier", KindInvalidIdentifier, "%q is not a valid identifier", p.Identifier)
			} else if seenIdent[up] {
				add(where+".identifier", KindDuplicate, "%q is listed twice", p.Identifier)
			}
			seenIdent[up] = true
		}

		seenMembers := map[string]bool{}
		for j, mem := range p.Members {
			path := fmt.Sprintf("%s.members[%d]", where, j)
			if validEmail(mem.Email) && NormalizeEmail(mem.Email) == importer {
				add(path+".email", KindImporterListed, "the importer must not be listed")
			} else {
				email(path+".email", mem.Email, seenMembers)
			}
			switch {
			case mem.Role == "":
				add(path+".role", KindRequired, "is required")
			case !validRole(mem.Role):
				add(path+".role", KindInvalidRole, "%q is not one of %s", mem.Role, strings.Join(projectRoles, ", "))
			}
		}

		for _, k := range sortedKeys(p.Sections) {
			if utf8.RuneCountInString(p.Sections[k]) > maxSectionRunes {
				add(fmt.Sprintf("%s.sections[%q]", where, k), KindTooLong, "is longer than %d characters", maxSectionRunes)
			}
		}
	}
	return errs
}
