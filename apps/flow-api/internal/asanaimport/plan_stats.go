package asanaimport

import (
	"sort"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
)

// NameCount is a name and how many tasks carry it.
type NameCount struct {
	Name  string
	Count int
}

// PersonStat is how one Asana user resolved in a project and how much they appear.
type PersonStat struct {
	GID, Name, Kind, Email                 string
	Assignee, Creator, Commenter, Follower int
}

// ProjectStats feeds the dry-run block of one project.
type ProjectStats struct {
	Tasks, TopLevel, Subtasks    int
	Default, Milestone, Approval int
	Completed                    int
	BySection                    []NameCount // Asana section order; "" maps to no label
	Tags                         []NameCount // by name
	Comments                     int
	AttachmentsAsana             int
	AttachmentsExternal          int
	Blocks                       int
	Skipped                      map[string]int // by Skip* reason, all entities
	People                       []PersonStat
	CustomFields                 int // set custom field values, not imported
	Likes, SystemStories         int
}

// stats fills each project's dry-run numbers.
func (p *Plan) stats() {
	type tagCount struct {
		name  string
		count int
	}
	tags := make([]map[string]*tagCount, len(p.Projects))
	sections := make([]map[string]int, len(p.Projects))
	type personKey struct {
		project int
		gid     string
	}
	persons := map[personKey]*PersonStat{}
	involve := func(project int, u *asana.User) *PersonStat {
		k := personKey{project, u.GID}
		if ps, ok := persons[k]; ok {
			return ps
		}
		name, ok := p.people.UserName(u.GID)
		if !ok {
			name = u.Name
		}
		kind, email := p.Projects[project].conv.classify(u.GID)
		ps := &PersonStat{GID: u.GID, Name: name, Kind: kind, Email: email}
		persons[k] = ps
		return ps
	}
	for i := range p.Projects {
		p.Projects[i].Stats.Skipped = map[string]int{}
		tags[i] = map[string]*tagCount{}
		sections[i] = map[string]int{}
	}

	for _, tp := range p.Tasks {
		st := &p.Projects[tp.Project].Stats
		d := p.snap.Tasks[tp.GID]
		t := &d.Task
		st.Tasks++
		if t.Parent == nil {
			st.TopLevel++
		} else {
			st.Subtasks++
		}
		switch t.ResourceSubtype {
		case "milestone":
			st.Milestone++
		case "approval":
			st.Approval++
		default:
			st.Default++
		}
		if t.Completed {
			st.Completed++
		}
		if s := p.Projects[tp.Project].conv.taskSection(t); s != nil {
			sections[tp.Project][s.GID]++
		}
		counted := map[string]bool{}
		for _, tag := range t.Tags {
			name := labelName(tag.Name)
			k := nameKey(name)
			if name == "" || counted[k] {
				continue
			}
			counted[k] = true
			if tags[tp.Project][k] == nil {
				tags[tp.Project][k] = &tagCount{name: name}
			}
			tags[tp.Project][k].count++
		}
		st.Comments += len(d.Comments)
		for _, a := range d.Attachments {
			if hosted(a) {
				st.AttachmentsAsana++
			} else {
				st.AttachmentsExternal++
			}
		}
		for _, f := range t.CustomFields {
			if f.DisplayValue != nil && *f.DisplayValue != "" {
				st.CustomFields++
			}
		}
		st.Likes += d.Likes
		st.SystemStories += d.SystemStories
		if t.Assignee != nil {
			involve(tp.Project, t.Assignee).Assignee++
		}
		if t.CreatedBy != nil {
			involve(tp.Project, t.CreatedBy).Creator++
		}
		for i := range t.Followers {
			involve(tp.Project, &t.Followers[i]).Follower++
		}
		for _, s := range d.Comments {
			if s.CreatedBy != nil {
				involve(tp.Project, s.CreatedBy).Commenter++
			}
		}
		if tp.Action == ActionSkip {
			st.Skipped[tp.Skip]++
		}
	}
	for _, c := range p.Comments {
		if c.Action == ActionSkip {
			p.Projects[c.Project].Stats.Skipped[c.Skip]++
		}
	}
	for _, a := range p.Attachments {
		if a.Action == ActionSkip {
			p.Projects[a.Project].Stats.Skipped[a.Skip]++
		}
	}
	for _, dp := range p.Dependencies {
		st := &p.Projects[dp.Project].Stats
		switch dp.Action {
		case ActionSkip:
			st.Skipped[dp.Skip]++
		case ActionCreate, ActionUnchanged:
			st.Blocks++
		}
	}

	for i, pp := range p.Projects {
		st := &pp.Stats
		if ps := p.snap.Mapped[pp.Source.GID]; ps != nil {
			for _, s := range ps.Sections {
				st.BySection = append(st.BySection, NameCount{Name: pp.conv.sectionLabel(s.Name), Count: sections[i][s.GID]})
			}
		}
		for _, tc := range tags[i] {
			st.Tags = append(st.Tags, NameCount{Name: tc.name, Count: tc.count})
		}
		sort.Slice(st.Tags, func(a, b int) bool { return st.Tags[a].Name < st.Tags[b].Name })
	}
	for k, ps := range persons {
		p.Projects[k.project].Stats.People = append(p.Projects[k.project].Stats.People, *ps)
	}
	for _, pp := range p.Projects {
		people := pp.Stats.People
		sort.Slice(people, func(a, b int) bool {
			if people[a].Name != people[b].Name {
				return people[a].Name < people[b].Name
			}
			return gidLess(people[a].GID, people[b].GID)
		})
	}
}
