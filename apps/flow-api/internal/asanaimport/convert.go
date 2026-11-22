package asanaimport

import (
	"database/sql"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/richtext"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

const (
	maxTitleRunes    = 500
	maxBodyRunes     = 50000 // tasks.description and comments.body
	maxFilenameRunes = 512
	dateLayout       = "2006-01-02"
	ellipsis         = "…"
	asanaHost        = "asana"
)

// Skip reasons, worded as the report prints them.
const (
	SkipEmptyTitle        = "empty title"
	SkipParentNotImported = "parent not imported"
	SkipEmptyBody         = "empty body"
	SkipTaskNotImported   = "task not imported"
	SkipTooLarge          = "too large"
	SkipTypeNotAllowed    = "type not allowed"
	SkipOutsideMapping    = "outside the mapping"
	SkipCycle             = "cycle"
)

// How an Asana user resolved within one project.
const (
	PersonExplicit  = "explicit"
	PersonEmail     = "email"
	PersonNotMember = "not-member"
	PersonUnmatched = "unmatched"
)

// people resolves Asana users to flow addresses by source_users, then by
// the Asana email. It is also the rich-text mention resolver.
type people struct {
	names map[string]string // gid -> Asana display name
	email map[string]string // gid -> normalized address of the Asana-mapped person
	how   map[string]string // gid -> PersonExplicit | PersonEmail
}

// newPeople matches users; known reports whether an address is an enabled
// account or one the mapping creates.
func newPeople(users []asana.User, sourceUsers map[string]string, known func(email string) bool) *people {
	p := &people{names: map[string]string{}, email: map[string]string{}, how: map[string]string{}}
	for _, u := range users {
		p.names[u.GID] = u.Name
		if u.Email != "" && known(NormalizeEmail(u.Email)) {
			p.email[u.GID] = NormalizeEmail(u.Email)
			p.how[u.GID] = PersonEmail
		}
	}
	for gid, email := range sourceUsers {
		p.email[gid] = NormalizeEmail(email)
		p.how[gid] = PersonExplicit
	}
	return p
}

// UserName implements richtext.Resolver.
func (p *people) UserName(gid string) (string, bool) {
	n, ok := p.names[gid]
	return n, ok
}

// mapped is the set of addresses an Asana user resolves to, whatever the project.
func (p *people) mapped() map[string]bool {
	out := map[string]bool{}
	for _, e := range p.email {
		out[e] = true
	}
	return out
}

var _ richtext.Resolver = (*people)(nil)

// LabelRef names a label. Project is the plan's project index for a
// project-scoped (section) label and -1 for a workspace (tag) label.
type LabelRef struct {
	Project int
	Name    string
}

// TaskValue is an Asana task in flow's terms. Addresses are normalized; an
// empty CreatedBy means the importer.
type TaskValue struct {
	Title       string
	Description string
	DueOn       sql.NullTime // UTC midnight of the date
	StartOn     sql.NullTime
	Completed   bool
	CreatedAt   time.Time
	CreatedBy   string
	Assignee    string
	Watchers    []string // sorted, assignee excluded
	Labels      []LabelRef
	// Truncated reports that the description was cut to maxBodyRunes.
	Truncated    bool
	StartDropped bool
}

// CommentValue is an Asana comment in flow's terms.
type CommentValue struct {
	// Markdown is the converted text, without the unresolved-author prefix.
	Markdown   string
	Author     string // normalized address; "" when unresolved
	AuthorName string // Asana display name, for the prefix
	CreatedAt  time.Time
}

// AttachmentValue is an Asana-hosted attachment in flow's terms.
type AttachmentValue struct {
	Name        string
	ContentType string
	Size        int64
	Skip        string
}

// projectConv converts tasks for one mapped project.
type projectConv struct {
	loc        *time.Location
	people     *people
	members    map[string]bool // addresses that count as project members, importer included
	project    int             // plan index, for section labels
	projectGID string
	sections   map[string]string
}

// person resolves an Asana user within the project; "" when unresolved.
func (pc *projectConv) person(u *asana.User) string {
	if u == nil {
		return ""
	}
	if e := pc.people.email[u.GID]; pc.members[e] {
		return e
	}
	return ""
}

// classify is the dry-run classification of an Asana user in this project.
func (pc *projectConv) classify(gid string) (kind, email string) {
	e, ok := pc.people.email[gid]
	switch {
	case !ok:
		return PersonUnmatched, ""
	case !pc.members[e]:
		return PersonNotMember, e
	default:
		return pc.people.how[gid], e
	}
}

// truncateRunes keeps s within n runes, ending a cut string with an ellipsis.
func truncateRunes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n-1]) + ellipsis, true
}

// cutRunes keeps the first n runes of s.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// labelName is the flow label name for an Asana tag or section name.
func labelName(s string) string { return cutRunes(s, maxSectionRunes) }

// sectionLabel is the label a section maps to; "" means none.
func (pc *projectConv) sectionLabel(section string) string {
	if v, ok := pc.sections[section]; ok {
		return labelName(v)
	}
	return labelName(section)
}

// taskSection is the task's section in this project, nil when it has none.
func (pc *projectConv) taskSection(t *asana.Task) *asana.Ref {
	for _, m := range t.Memberships {
		if m.Project.GID == pc.projectGID && m.Section != nil {
			return m.Section
		}
	}
	return nil
}

// parseDay reads Asana's date-only and datetime forms; the date wins.
func (pc *projectConv) parseDay(on string, at *time.Time) sql.NullTime {
	if on != "" {
		if d, err := time.Parse(dateLayout, on); err == nil {
			return sql.NullTime{Time: d, Valid: true}
		}
	}
	if at != nil {
		d, _ := time.Parse(dateLayout, at.In(pc.loc).Format(dateLayout))
		return sql.NullTime{Time: d, Valid: true}
	}
	return sql.NullTime{}
}

// hosted reports whether the file itself can be copied from Asana.
func hosted(a asana.Attachment) bool { return a.Host == asanaHost && a.DownloadURL != "" }

// gidLess orders Asana gids numerically.
func gidLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// task converts d. A non-empty skip reason means the task is not imported.
func (pc *projectConv) task(d *asana.TaskDetail) (v TaskValue, skip string) {
	t := &d.Task
	title := strings.TrimSpace(t.Name)
	if title == "" {
		return v, SkipEmptyTitle
	}
	var parts []string
	if cut, ok := truncateRunes(title, maxTitleRunes); ok {
		parts = append(parts, "**"+title+"**")
		title = cut
	}
	v.Title = title
	if body := richtext.ToMarkdown(t.HTMLNotes, pc.people); body != "" {
		parts = append(parts, body)
	}
	var links []asana.Attachment
	for _, a := range d.Attachments {
		if !hosted(a) {
			links = append(links, a)
		}
	}
	if len(links) > 0 {
		sort.Slice(links, func(i, j int) bool { return gidLess(links[i].GID, links[j].GID) })
		lines := make([]string, len(links))
		for i, a := range links {
			lines[i] = "- [" + a.Name + "](" + a.ViewURL + ")"
		}
		parts = append(parts, "---\n"+strings.Join(lines, "\n"))
	}
	v.Description, v.Truncated = truncateRunes(strings.Join(parts, "\n\n"), maxBodyRunes)

	v.DueOn = pc.parseDay(t.DueOn, t.DueAt)
	v.StartOn = pc.parseDay(t.StartOn, t.StartAt)
	if v.DueOn.Valid && v.StartOn.Valid && v.StartOn.Time.After(v.DueOn.Time) {
		v.StartOn, v.StartDropped = sql.NullTime{}, true
	}
	v.Completed = t.Completed
	v.CreatedAt = t.CreatedAt
	v.CreatedBy = pc.person(t.CreatedBy)
	v.Assignee = pc.person(t.Assignee)
	seen := map[string]bool{v.Assignee: true, "": true}
	for i := range t.Followers {
		if e := pc.person(&t.Followers[i]); !seen[e] {
			seen[e] = true
			v.Watchers = append(v.Watchers, e)
		}
	}
	sort.Strings(v.Watchers)

	if s := pc.taskSection(t); s != nil {
		if name := pc.sectionLabel(s.Name); name != "" {
			v.Labels = append(v.Labels, LabelRef{Project: pc.project, Name: name})
		}
	}
	keys := map[string]bool{}
	for _, tag := range t.Tags {
		name := labelName(tag.Name)
		if k := nameKey(name); name != "" && !keys[k] {
			keys[k] = true
			v.Labels = append(v.Labels, LabelRef{Project: -1, Name: name})
		}
	}
	return v, ""
}

// comment converts s; an empty Markdown means the comment is skipped.
func (pc *projectConv) comment(s asana.Story) CommentValue {
	v := CommentValue{
		Markdown:  richtext.ToMarkdown(s.HTMLText, pc.people),
		Author:    pc.person(s.CreatedBy),
		CreatedAt: s.CreatedAt,
	}
	if s.CreatedBy != nil {
		v.AuthorName = s.CreatedBy.Name
	}
	if v.AuthorName == "" {
		v.AuthorName = "unknown user"
	}
	return v
}

// body is the stored comment text; prefixed names the Asana author for a
// comment filed under the importer.
func (v CommentValue) body(prefixed bool) (string, bool) {
	s := v.Markdown
	if prefixed {
		s = "**" + v.AuthorName + ":** " + s
	}
	return truncateRunes(s, maxBodyRunes)
}

// truncateFilename keeps the extension and cuts the stem to fit.
func truncateFilename(name string) string {
	if utf8.RuneCountInString(name) <= maxFilenameRunes {
		return name
	}
	ext := filepath.Ext(name)
	keep := maxFilenameRunes - utf8.RuneCountInString(ext)
	if keep <= 0 {
		return cutRunes(name, maxFilenameRunes)
	}
	return cutRunes(strings.TrimSuffix(name, ext), keep) + ext
}

// attachment converts an Asana-hosted attachment, deciding by name and the
// listed size so the answer never needs a download.
func attachment(a asana.Attachment) AttachmentValue {
	v := AttachmentValue{Name: truncateFilename(a.Name), Size: a.Size}
	v.ContentType = storage.GenericBinaryType
	if ct, _, err := mime.ParseMediaType(mime.TypeByExtension(strings.ToLower(filepath.Ext(a.Name)))); err == nil {
		v.ContentType = ct
	}
	switch {
	case !storage.IsAllowedUpload(v.ContentType, a.Name):
		v.Skip = SkipTypeNotAllowed
	case a.Size > storage.MaxUploadSize:
		v.Skip = SkipTooLarge
	}
	return v
}
