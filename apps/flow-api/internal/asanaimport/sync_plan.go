package asanaimport

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
)

// maxUnguardedDeletes is how many tasks one project may lose whatever its
// size: small losses are accepted, as a soft delete can be undone.
const maxUnguardedDeletes = 10

// Guard refusal reasons, as the shared fixtures name them.
const (
	RefuseEmptySource = "empty-source"
	RefuseMassDelete  = "mass-delete"
)

// SyncCounts are the numbers of one sync plan line.
type SyncCounts struct {
	Create, Update, Unchanged, Delete, Moved, Kept, Synced, Orphaned, Repeated int
}

// GuardInput are the deletion guard's values for one project.
type GuardInput struct {
	Present, Synced, Delete, Moved int
}

// Refusal is a project the deletion guard stops.
type Refusal struct {
	Project int
	Name    string
	Reason  string // RefuseEmptySource or RefuseMassDelete
	Lost    int
	Synced  int
}

// Prober answers single-item existence checks; *asana.Client is one.
type Prober interface {
	ProbeTask(ctx context.Context, gid string) asana.ProbeResult
	ProbeStory(ctx context.Context, gid string) asana.ProbeResult
}

// Guard decides the deletion guard for one project.
func Guard(g GuardInput) string {
	lost := g.Delete - g.Moved
	switch {
	case g.Present == 0 && g.Synced > 0 && lost > 0:
		return RefuseEmptySource
	case lost > maxUnguardedDeletes && lost*2 > g.Synced:
		return RefuseMassDelete
	}
	return ""
}

// synced is the number of this importer's live rows in the project.
func (p *Plan) synced(project int) int {
	id := p.Projects[project].ExistingID
	if id == 0 {
		return 0
	}
	n := 0
	for _, t := range p.existing.Tasks {
		if t.ProjectID == id {
			n++
		}
	}
	return n
}

// GuardInput gathers the guard's values for one project.
func (p *Plan) GuardInput(project int) GuardInput {
	g := GuardInput{Present: len(p.Projects[project].listed), Synced: p.synced(project)}
	for _, d := range p.TaskDeletions {
		if d.Project == project && d.Candidate {
			g.Delete++
			if d.Moved {
				g.Moved++
			}
		}
	}
	return g
}

// Refusals lists the projects a sync must not touch. Without -sync nothing is deleted.
func (p *Plan) Refusals() []Refusal {
	if !p.Sync {
		return nil
	}
	var out []Refusal
	for i, pp := range p.Projects {
		g := p.GuardInput(i)
		if reason := Guard(g); reason != "" {
			out = append(out, Refusal{Project: i, Name: pp.Name, Reason: reason, Lost: g.Delete - g.Moved, Synced: g.Synced})
		}
	}
	return out
}

// Probe asks Asana about every deletion candidate of the projects the
// guard passed. A task gone or visible outside the mapping is deleted; a
// comment only when gone. Anything else is kept.
func (p *Plan) Probe(ctx context.Context, pr Prober) {
	refused := map[int]bool{}
	for _, r := range p.Refusals() {
		refused[r.Project] = true
	}
	for _, d := range p.TaskDeletions {
		if d.Moved || refused[d.Project] {
			continue
		}
		d.Probe, d.Probed = pr.ProbeTask(ctx, d.Row.GID), true
		d.Delete = d.Probe == asana.NotFound || d.Probe == asana.Exists
	}
	for _, d := range p.CommentDeletions {
		if refused[d.Project] {
			continue
		}
		d.Probe, d.Probed = pr.ProbeStory(ctx, d.Row.GID), true
		d.Delete = d.Probe == asana.NotFound
	}
}

// TaskSync is the tasks sync plan line of one project, after Probe.
// Orphaned rows count as kept as well.
func (p *Plan) TaskSync(project int) SyncCounts {
	pp := p.Projects[project]
	var s SyncCounts
	for _, tp := range p.Tasks {
		if tp.Project == project {
			s.count(tp.Action)
		}
	}
	for _, d := range p.TaskDeletions {
		if d.Project != project {
			continue
		}
		switch {
		case d.Moved:
			s.Moved++
		case d.Delete:
			s.Delete++
		case d.Probed:
			s.Kept++
		}
	}
	s.Synced = p.synced(project)
	if pp.ExistingID != 0 {
		s.Orphaned = max(0, p.existing.SourceUIDCounts[pp.ExistingID]-s.Synced)
	}
	s.Kept += s.Orphaned
	if ps := p.snap.Mapped[pp.Source.GID]; ps != nil {
		seen := map[asana.Repeat]bool{}
		for _, r := range ps.Repeated {
			seen[r] = true
		}
		s.Repeated = len(seen)
	}
	return s
}

// CommentSync is the comments sync plan line of one project, after Probe.
func (p *Plan) CommentSync(project int) SyncCounts {
	var s SyncCounts
	for _, c := range p.Comments {
		if c.Project == project {
			s.count(c.Action)
		}
	}
	for _, d := range p.CommentDeletions {
		if d.Project != project {
			continue
		}
		switch {
		case d.Delete:
			s.Delete++
		case d.Probed:
			s.Kept++
		}
	}
	for _, tp := range p.Tasks {
		if tp.Project == project && tp.Row != nil && tp.Action != ActionSkip {
			s.Synced += len(p.existing.Comments[tp.Row.ID])
		}
	}
	return s
}

func (s *SyncCounts) count(a Action) {
	switch a {
	case ActionCreate:
		s.Create++
	case ActionUpdate:
		s.Update++
	case ActionUnchanged:
		s.Unchanged++
	case ActionKeep:
		s.Kept++
	}
}
