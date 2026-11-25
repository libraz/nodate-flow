package asanaimport

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// KindUnresolved marks a stage-2 problem: the mapping disagrees with Asana
// or the database.
const KindUnresolved = "unresolved"

// Input is what resolution and planning read.
type Input struct {
	Mapping *Mapping
	Sync    bool
	// Workspaces are the Asana workspaces the token sees.
	Workspaces []asana.Workspace
	// Snapshot is nil when source_workspace did not resolve.
	Snapshot *asana.Snapshot
	// AllowedDomains is NF_OAUTH_ALLOWED_DOMAINS, already split.
	AllowedDomains    []string
	StorageConfigured bool
}

// Account is an existing flow account the run refers to.
type Account struct {
	ID       uint32
	PublicID types.PublicID
	Email    string // normalized
	Name     string
	Enabled  bool
}

// SignIn is the sign-in restriction state, read during resolution.
type SignIn struct {
	Domains   int
	Allowlist int64
}

// Restricted reports whether created accounts need an allowlist row.
func (s SignIn) Restricted() bool { return s.Domains > 0 || s.Allowlist > 0 }

// MemberPlan is one mapped project member.
type MemberPlan struct {
	Email string // normalized
	Role  generated.ProjectMembersRole
	// Existing is set when the reused project already has the account.
	Existing bool
}

// Notes are the per-project counters outside the entity counters.
// CompletionGate is only known while writing.
type Notes struct {
	Truncated                 int
	StartDropped              int
	ParentChanged             int
	AttachmentsRemovedInAsana int
	LabelsLeft                int
	CompletionGate            int
	CancelledKept             int
	SlugIgnored               int
	IdentifierIgnored         int
}

// ProjectPlan is one mapped project after resolution.
type ProjectPlan struct {
	Index   int // position in the mapping
	Mapping MappingProject
	Source  asana.Project
	Name    string
	// ExistingID is the reused flow project; 0 means it is created.
	ExistingID uint32
	// Slug and Identifier are used on creation; Identifier is upper case.
	Slug       string
	Identifier string
	Members    []MemberPlan
	Notes      Notes
	Stats      ProjectStats

	conv   *projectConv
	listed map[string]bool // gids Asana lists for the project, descendants included
}

// resolver carries the state of one resolution pass.
type resolver struct {
	ctx  context.Context
	lk   lookup
	in   Input
	m    *Mapping
	plan *Plan
	// creating holds the normalized addresses of mapping users that do not exist yet.
	creating map[string]bool
	roles    map[uint32]bool // account id -> live workspace member
	byID     map[uint32]string
	// projectMembers is the addresses of each reused project's members, by project id.
	projectMembers map[uint32]map[string]bool
	flowNames      map[uint32]string // enabled flow project names by id
}

func (r *resolver) add(path, format string, a ...any) {
	r.plan.Errors = append(r.plan.Errors, MappingError{Path: path, Kind: KindUnresolved, Detail: fmt.Sprintf(format, a...)})
}

// ResolveSourceWorkspace picks the Asana workspace the mapping names, or
// the only one the token sees.
func ResolveSourceWorkspace(m *Mapping, all []asana.Workspace) (asana.Workspace, *MappingError) {
	var hits []asana.Workspace
	for _, w := range all {
		if m.SourceWorkspace == "" || w.GID == m.SourceWorkspace || w.Name == m.SourceWorkspace {
			hits = append(hits, w)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return asana.Workspace{}, &MappingError{Path: "source_workspace", Kind: KindUnresolved, Detail: "matches no Asana workspace"}
	default:
		return asana.Workspace{}, &MappingError{Path: "source_workspace", Kind: KindUnresolved, Detail: "matches more than one Asana workspace"}
	}
}

// ResolveProjectSources matches each mapped source against the Asana
// projects, archived included. Index i of the result is projects[i]; a
// zero Project means it did not resolve, and errs says why.
func ResolveProjectSources(m *Mapping, all []asana.Project) (out []asana.Project, errs []MappingError) {
	out = make([]asana.Project, len(m.Projects))
	for i, mp := range m.Projects {
		if mp.Source == "" {
			continue
		}
		var hits []asana.Project
		for _, p := range all {
			if p.GID == mp.Source || p.Name == mp.Source {
				hits = append(hits, p)
			}
		}
		path := fmt.Sprintf("projects[%d].source", i)
		switch len(hits) {
		case 1:
			out[i] = hits[0]
		case 0:
			errs = append(errs, MappingError{Path: path, Kind: KindUnresolved, Detail: "matches no Asana project"})
		default:
			errs = append(errs, MappingError{Path: path, Kind: KindUnresolved, Detail: "matches more than one Asana project"})
		}
	}
	return out, errs
}

// emailRef is one place the mapping names an address.
type emailRef struct {
	path  string
	email string // normalized
}

// refs lists the addresses in document order, importer excluded.
func (m *Mapping) refs() []emailRef {
	var out []emailRef
	for i, u := range m.Users {
		out = append(out, emailRef{fmt.Sprintf("users[%d].email", i), NormalizeEmail(u.Email)})
	}
	for _, k := range sortedKeys(m.SourceUsers) {
		out = append(out, emailRef{fmt.Sprintf("source_users[%q]", k), NormalizeEmail(m.SourceUsers[k])})
	}
	for i, p := range m.Projects {
		for j, mem := range p.Members {
			out = append(out, emailRef{fmt.Sprintf("projects[%d].members[%d].email", i, j), NormalizeEmail(mem.Email)})
		}
	}
	return out
}

// Prepare resolves the mapping against Asana and the database and plans
// every write. Problems with the mapping land in Plan.Errors; the error
// return is for failed reads only. It never writes.
func Prepare(ctx context.Context, lk lookup, in Input) (*Plan, error) {
	r := &resolver{
		ctx: ctx, lk: lk, in: in, m: in.Mapping,
		plan: &Plan{
			Sync: in.Sync, Accounts: map[string]Account{}, Location: time.UTC,
			existing: emptyExisting(), labels: labelIndex{},
		},
		creating: map[string]bool{}, roles: map[uint32]bool{}, byID: map[uint32]string{},
		projectMembers: map[uint32]map[string]bool{}, flowNames: map[uint32]string{},
	}
	if err := r.workspace(); err != nil {
		return nil, err
	}
	if err := r.accounts(); err != nil {
		return nil, err
	}
	if err := r.people(); err != nil {
		return nil, err
	}
	if in.Snapshot != nil {
		if err := r.projects(); err != nil {
			return nil, err
		}
		r.plan.build(in.Snapshot)
		r.placementErrors()
	}
	return r.plan, nil
}

func (r *resolver) workspace() error {
	p, m := r.plan, r.m
	if sw, err := ResolveSourceWorkspace(m, r.in.Workspaces); err != nil {
		p.Errors = append(p.Errors, *err)
	} else {
		p.SourceWorkspace = sw
	}
	ws, found, err := r.lk.workspace(r.ctx, m.Workspace)
	if err != nil {
		return fmt.Errorf("read workspace: %w", err)
	}
	if !found {
		r.add("workspace", "no workspace has slug %q", m.Workspace)
		return nil
	}
	p.Workspace, p.WorkspaceFound = ws, true
	if p.Location, err = time.LoadLocation(ws.Timezone); err != nil {
		return fmt.Errorf("workspace timezone %q: %w", ws.Timezone, err)
	}
	if p.SignIn.Allowlist, err = r.lk.allowlistCount(r.ctx); err != nil {
		return fmt.Errorf("count sign-in allowlist: %w", err)
	}
	p.SignIn.Domains = len(r.in.AllowedDomains)
	if p.existing, err = r.lk.existing(r.ctx, ws.ID); err != nil {
		return fmt.Errorf("read imported rows: %w", err)
	}
	rows, err := r.lk.labels(r.ctx, ws.ID)
	if err != nil {
		return fmt.Errorf("read labels: %w", err)
	}
	for _, l := range rows {
		p.labels.add(uint32(l.ProjectID.Int32), l.Name, labelEntry{l.ID, l.PublicID}) //#nosec G115 -- generated NullInt32 of an INT UNSIGNED project id
	}
	return nil
}

// member reports whether the account is a live workspace member; false without a workspace.
func (r *resolver) member(id uint32) (bool, error) {
	if !r.plan.WorkspaceFound {
		return false, nil
	}
	if ok, seen := r.roles[id]; seen {
		return ok, nil
	}
	_, ok, err := r.lk.workspaceRole(r.ctx, r.plan.Workspace.ID, id)
	if err != nil {
		return false, fmt.Errorf("read workspace membership: %w", err)
	}
	r.roles[id] = ok
	return ok, nil
}

// accounts reads every address the mapping or Asana names and checks the
// mapping's references against them.
func (r *resolver) accounts() error {
	p, m := r.plan, r.m
	want := map[string]bool{}
	if validEmail(m.Importer) {
		want[NormalizeEmail(m.Importer)] = true
	}
	for _, ref := range m.refs() {
		if validEmail(ref.email) {
			want[ref.email] = true
		}
	}
	if r.in.Snapshot != nil {
		for _, u := range r.in.Snapshot.Users {
			if validEmail(u.Email) {
				want[NormalizeEmail(u.Email)] = true
			}
		}
	}
	emails := make([]string, 0, len(want))
	for e := range want {
		emails = append(emails, e)
	}
	slices.Sort(emails)
	if len(emails) > 0 {
		rows, err := r.lk.usersByEmail(r.ctx, emails)
		if err != nil {
			return fmt.Errorf("read accounts: %w", err)
		}
		for _, u := range rows {
			a := Account{ID: u.ID, PublicID: u.PublicID, Email: NormalizeEmail(u.Email), Name: u.DisplayName, Enabled: u.Enabled}
			p.Accounts[a.Email] = a
			r.byID[a.ID] = a.Email
		}
	}

	importer := NormalizeEmail(m.Importer)
	if p.WorkspaceFound {
		ok := false
		if a, exists := p.Accounts[importer]; exists && a.Enabled {
			role, member, err := r.lk.workspaceRole(r.ctx, p.Workspace.ID, a.ID)
			if err != nil {
				return fmt.Errorf("read importer membership: %w", err)
			}
			ok = member && (role == generated.WorkspaceMembersRoleOwner || role == generated.WorkspaceMembersRoleAdmin)
			p.Importer = a
		}
		if !ok {
			r.add("importer", "%q is not an enabled owner or admin of workspace %q", importer, m.Workspace)
		}
	}

	for i, u := range m.Users {
		e := NormalizeEmail(u.Email)
		if a, exists := p.Accounts[e]; exists {
			if !a.Enabled {
				continue // reported as a disabled reference below
			}
			if ok, err := r.member(a.ID); err != nil {
				return err
			} else if !ok && p.WorkspaceFound {
				r.add(fmt.Sprintf("users[%d].email", i), "%q is an existing account that is not a member of workspace %q", e, m.Workspace)
			}
			continue
		}
		if validEmail(e) && !r.creating[e] {
			r.creating[e] = true
			name := u.Name
			if name == "" {
				name, _, _ = strings.Cut(u.Email, "@")
			}
			p.NewUsers = append(p.NewUsers, MappingUser{Email: e, Name: name})
		}
	}

	seen := map[string]bool{importer: true}
	for _, ref := range m.refs() {
		if seen[ref.email] || !validEmail(ref.email) {
			continue
		}
		seen[ref.email] = true
		a, exists := p.Accounts[ref.email]
		switch {
		case !exists && !r.creating[ref.email]:
			r.add(ref.path, "%q is not in users and is not an existing account", ref.email)
		case exists && !a.Enabled:
			r.add(ref.path, "%q is a disabled account", ref.email)
		}
	}

	for i, mp := range m.Projects {
		for j, mem := range mp.Members {
			e := NormalizeEmail(mem.Email)
			a, exists := p.Accounts[e]
			if !exists || !a.Enabled || r.creating[e] || e == importer || !p.WorkspaceFound {
				continue
			}
			if ok, err := r.member(a.ID); err != nil {
				return err
			} else if !ok {
				r.add(fmt.Sprintf("projects[%d].members[%d].email", i, j), "%q is not a member of workspace %q", e, m.Workspace)
			}
		}
	}
	return nil
}

// people builds the Asana user resolution and checks source_users keys.
func (r *resolver) people() error {
	p, snap := r.plan, r.in.Snapshot
	var users []asana.User
	if snap != nil {
		users = snap.Users
	}
	p.people = newPeople(users, r.m.SourceUsers, func(e string) bool {
		a, ok := p.Accounts[e]
		return ok && a.Enabled || r.creating[e]
	})
	if snap == nil {
		return nil
	}
	known := referencedUsers(snap)
	for _, u := range snap.Users {
		known[u.GID] = true
	}
	for _, k := range sortedKeys(r.m.SourceUsers) {
		if isSourceUserID(k) && !known[k] {
			r.add(fmt.Sprintf("source_users[%q]", k), "is not a user of Asana workspace %q", p.SourceWorkspace.Name)
		}
	}
	return nil
}

// referencedUsers is the set of user gids the fetched tasks and comments
// name. It covers people who have left the workspace: Asana omits them
// from the user listing and their embedded records carry no email, so
// source_users is the only way to map them.
func referencedUsers(snap *asana.Snapshot) map[string]bool {
	out := map[string]bool{}
	add := func(u *asana.User) {
		if u != nil && u.GID != "" {
			out[u.GID] = true
		}
	}
	for _, d := range snap.Tasks {
		add(d.Task.Assignee)
		add(d.Task.CreatedBy)
		for i := range d.Task.Followers {
			add(&d.Task.Followers[i])
		}
		for i := range d.Comments {
			add(d.Comments[i].CreatedBy)
		}
	}
	return out
}

// projects resolves each mapped project and decides between reuse and creation.
func (r *resolver) projects() error {
	p, m, snap := r.plan, r.m, r.in.Snapshot
	sources, errs := ResolveProjectSources(m, snap.Projects)
	p.Errors = append(p.Errors, errs...)

	firstSource := map[string]int{}
	for i, src := range sources {
		if src.GID == "" {
			continue
		}
		mp := m.Projects[i]
		if j, dup := firstSource[src.GID]; dup {
			r.add(fmt.Sprintf("projects[%d].source", i), "resolves to the same Asana project as projects[%d]", j)
			continue
		}
		firstSource[src.GID] = i
		pp := &ProjectPlan{Index: i, Mapping: mp, Source: src, Name: mp.Name, listed: map[string]bool{}}
		if pp.Name == "" {
			pp.Name = cutRunes(src.Name, maxNameRunes)
		}
		ps := snap.Mapped[src.GID]
		sections := map[string]bool{}
		if ps != nil {
			for _, g := range ps.Order {
				pp.listed[g] = true
			}
			for _, s := range ps.Sections {
				sections[s.Name] = true
			}
		}
		for _, k := range sortedKeys(mp.Sections) {
			if !sections[k] {
				r.add(fmt.Sprintf("projects[%d].sections[%q]", i, k), "is not a section of Asana project %q", src.Name)
			}
		}
		p.Projects = append(p.Projects, pp)
	}

	firstName := map[string]*ProjectPlan{}
	for _, pp := range p.Projects {
		k := nameKey(pp.Name)
		if prev, dup := firstName[k]; dup {
			if prev.Mapping.Name == "" || pp.Mapping.Name == "" || prev.Mapping.Name != pp.Mapping.Name {
				r.add(fmt.Sprintf("projects[%d].name", pp.Index), "resolves to the same name as projects[%d]", prev.Index)
			}
			continue
		}
		firstName[k] = pp
	}

	if err := r.flowProjects(); err != nil {
		return err
	}

	importer := NormalizeEmail(m.Importer)
	for idx, pp := range p.Projects {
		members := map[string]bool{importer: true}
		for _, mem := range pp.Mapping.Members {
			e := NormalizeEmail(mem.Email)
			members[e] = true
			pp.Members = append(pp.Members, MemberPlan{Email: e, Role: generated.ProjectMembersRole(mem.Role)})
		}
		for e := range r.projectMembers[pp.ExistingID] {
			members[e] = true
		}
		for k := range pp.Members {
			pp.Members[k].Existing = r.projectMembers[pp.ExistingID][pp.Members[k].Email]
		}
		pp.conv = &projectConv{
			loc: p.Location, people: p.people, members: members,
			project: idx, projectGID: pp.Source.GID, sections: pp.Mapping.Sections,
		}
	}
	return nil
}

// flowProjects matches each mapped project to an enabled flow project of
// the same name, or plans its creation.
func (r *resolver) flowProjects() error {
	p := r.plan
	for _, pp := range p.Projects {
		pp.Slug = pp.Mapping.Slug
		if pp.Slug == "" {
			pp.Slug = "asana-" + pp.Source.GID
		}
		pp.Identifier = strings.ToUpper(pp.Mapping.Identifier)
	}
	if !p.WorkspaceFound {
		return nil
	}
	rows, err := r.lk.projects(r.ctx, p.Workspace.ID)
	if err != nil {
		return fmt.Errorf("read projects: %w", err)
	}
	byName := map[string][]generated.ListProjectsForImportRow{}
	slugs, idents := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		byName[nameKey(row.Name)] = append(byName[nameKey(row.Name)], row)
		r.flowNames[row.ID] = row.Name
		slugs[row.Slug] = true
		if row.Identifier.Valid {
			idents[strings.ToUpper(row.Identifier.String)] = true
		}
	}
	var candidates []uint32
	for _, pp := range p.Projects {
		if hits := byName[nameKey(pp.Name)]; len(hits) == 1 {
			candidates = append(candidates, hits[0].ID)
		}
	}
	leads := map[uint32]map[uint32]bool{}
	if len(candidates) > 0 {
		members, err := r.lk.projectMembers(r.ctx, p.Workspace.ID, candidates)
		if err != nil {
			return fmt.Errorf("read project members: %w", err)
		}
		for _, mem := range members {
			if mem.Role == generated.ProjectMembersRoleLead {
				if leads[mem.ProjectID] == nil {
					leads[mem.ProjectID] = map[uint32]bool{}
				}
				leads[mem.ProjectID][mem.UserID] = true
			}
			if e, ok := r.byID[mem.UserID]; ok {
				if r.projectMembers[mem.ProjectID] == nil {
					r.projectMembers[mem.ProjectID] = map[string]bool{}
				}
				r.projectMembers[mem.ProjectID][e] = true
			}
		}
	}

	for _, pp := range p.Projects {
		where := fmt.Sprintf("projects[%d]", pp.Index)
		hits := byName[nameKey(pp.Name)]
		switch {
		case len(hits) > 1:
			r.add(where+".name", "matches more than one flow project")
		case len(hits) == 1 && (p.Importer.ID == 0 || !leads[hits[0].ID][p.Importer.ID]):
			r.add(where+".name", "matches flow project %q whose lead is not the importer", hits[0].Name)
		case len(hits) == 1:
			pp.ExistingID = hits[0].ID
			if pp.Mapping.Slug != "" {
				pp.Notes.SlugIgnored = 1
			}
			if pp.Mapping.Identifier != "" {
				pp.Notes.IdentifierIgnored = 1
			}
		default:
			if slugs[pp.Slug] {
				r.add(where+".slug", "%q is already used in workspace %q", pp.Slug, r.m.Workspace)
			}
			if pp.Identifier != "" && idents[pp.Identifier] {
				r.add(where+".identifier", "%q is already used in workspace %q", pp.Mapping.Identifier, r.m.Workspace)
			}
		}
	}
	return nil
}

// placementErrors reports tasks already imported into a project the
// mapping does not list, and attachments with nowhere to go.
func (r *resolver) placementErrors() {
	p := r.plan
	mapped := map[uint32]bool{}
	for _, pp := range p.Projects {
		if pp.ExistingID != 0 {
			mapped[pp.ExistingID] = true
		}
	}
	type key struct {
		project int
		flowID  uint32
	}
	first := map[key]string{}
	var order []key
	for _, row := range p.existing.Tasks {
		idx, placed := p.placed[row.GID]
		if !placed || mapped[row.ProjectID] {
			continue
		}
		if _, enabled := r.flowNames[row.ProjectID]; !enabled {
			continue
		}
		k := key{idx, row.ProjectID}
		if g, seen := first[k]; !seen {
			order = append(order, k)
			first[k] = row.GID
		} else if gidLess(row.GID, g) {
			first[k] = row.GID
		}
	}
	for _, k := range order {
		r.add(fmt.Sprintf("projects[%d].source", p.Projects[k.project].Index),
			"task %s is already imported into project %q, which this mapping does not list", first[k], r.flowNames[k.flowID])
	}

	if r.in.StorageConfigured {
		return
	}
	pending := make([]int, len(p.Projects))
	for _, a := range p.Attachments {
		if a.Action == ActionCreate {
			pending[a.Project]++
		}
	}
	for i, n := range pending {
		if n > 0 {
			r.add(fmt.Sprintf("projects[%d].source", p.Projects[i].Index),
				"has %d attachments but storage is not configured (NF_S3_ENDPOINT)", n)
		}
	}
}
