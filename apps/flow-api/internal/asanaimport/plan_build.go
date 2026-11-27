package asanaimport

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

func parentOf(snap *asana.Snapshot, gid string) string {
	if d := snap.Tasks[gid]; d != nil && d.Task.Parent != nil {
		return d.Task.Parent.GID
	}
	return ""
}

func hasRowIn(rows []*ExistingTask, projectID uint32) bool {
	for _, r := range rows {
		if r.ProjectID == projectID {
			return true
		}
	}
	return false
}

// place puts every listed task in one project and returns the gids with
// parents before children.
func (p *Plan) place(snap *asana.Snapshot, rows map[string][]*ExistingTask) []string {
	p.placed = map[string]int{}
	inScope := func(g string) bool {
		for _, pp := range p.Projects {
			if pp.listed[g] {
				return true
			}
		}
		return false
	}
	visiting := map[string]bool{}
	var placeOf func(g string) (int, bool)
	placeOf = func(g string) (int, bool) {
		if i, ok := p.placed[g]; ok {
			return i, true
		}
		if visiting[g] || snap.Tasks[g] == nil {
			return 0, false
		}
		visiting[g] = true
		defer delete(visiting, g)
		for i, pp := range p.Projects {
			if pp.ExistingID != 0 && pp.listed[g] && hasRowIn(rows[g], pp.ExistingID) {
				p.placed[g] = i
				return i, true
			}
		}
		if par := parentOf(snap, g); par != "" && inScope(par) {
			if i, ok := placeOf(par); ok {
				p.placed[g] = i
				return i, true
			}
		}
		for i, pp := range p.Projects {
			if pp.listed[g] {
				p.placed[g] = i
				return i, true
			}
		}
		return 0, false
	}

	var order []string
	emitted := map[string]bool{}
	var emit func(g string)
	emit = func(g string) {
		if emitted[g] {
			return
		}
		emitted[g] = true
		if par := parentOf(snap, g); par != "" {
			if pi, ok := p.placed[par]; ok && pi == p.placed[g] {
				emit(par)
			}
		}
		order = append(order, g)
	}
	for _, pp := range p.Projects {
		if ps := snap.Mapped[pp.Source.GID]; ps != nil {
			for _, g := range ps.Order {
				placeOf(g)
			}
		}
	}
	for i, pp := range p.Projects {
		if ps := snap.Mapped[pp.Source.GID]; ps != nil {
			for _, g := range ps.Order {
				if pi, ok := p.placed[g]; ok && pi == i {
					emit(g)
				}
			}
		}
	}
	return order
}

// build plans every entity from the snapshot and the existing rows.
func (p *Plan) build(snap *asana.Snapshot) {
	p.snap = snap
	p.taskByGID = map[string]*TaskPlan{}
	p.labelSets = map[int]map[uint32]bool{}
	rows := map[string][]*ExistingTask{}
	for _, t := range p.existing.Tasks {
		rows[t.GID] = append(rows[t.GID], t)
	}
	byProject := map[uint32]int{}
	for i, pp := range p.Projects {
		if pp.ExistingID != 0 {
			byProject[pp.ExistingID] = i
		}
	}
	for _, gid := range p.place(snap, rows) {
		p.planTask(gid, rows, byProject)
	}
	if p.Sync {
		p.planTaskDeletions(byProject)
	}
	for _, tp := range p.Tasks {
		p.planChildren(tp)
	}
	p.planDependencies()
	p.labelNotes()
	p.collectNewLabels()
	p.stats()
}

// sameProjectParent is the plan of the task's parent when both sit in one project.
func (p *Plan) sameProjectParent(tp *TaskPlan) *TaskPlan {
	par := parentOf(p.snap, tp.GID)
	if pi, ok := p.placed[par]; ok && par != "" && pi == tp.Project {
		return p.taskByGID[par]
	}
	return nil
}

func (p *Plan) planTask(gid string, rows map[string][]*ExistingTask, byProject map[uint32]int) {
	pp := p.Projects[p.placed[gid]]
	d := p.snap.Tasks[gid]
	tp := &TaskPlan{GID: gid, Project: p.placed[gid], Subtype: d.Task.ResourceSubtype}
	p.Tasks = append(p.Tasks, tp)
	p.taskByGID[gid] = tp
	v, skip := pp.conv.task(d)
	tp.Value = v

	var row, other *ExistingTask
	for _, r := range rows[gid] {
		if pp.ExistingID != 0 && r.ProjectID == pp.ExistingID {
			row = r
		} else if _, mapped := byProject[r.ProjectID]; mapped && other == nil {
			other = r
		}
	}
	parent := p.sameProjectParent(tp)
	switch {
	case row != nil:
		tp.Row = row
		p.noteParent(pp, row, parent)
		switch {
		case skip != "" && p.Sync:
			tp.Action, tp.Skip = ActionKeep, skip
		case skip != "" || !p.Sync:
			tp.Action = ActionUnchanged
		default:
			tp.Change = p.diff(pp, row, v)
			tp.Action = ActionUnchanged
			if !tp.Change.empty() {
				tp.Action = ActionUpdate
			}
		}
	case skip != "":
		tp.Action, tp.Skip = ActionSkip, skip
	case other != nil && !p.Sync:
		tp.Action, tp.Row, tp.MidMove = ActionUnchanged, other, true
	case parent != nil && (parent.Action == ActionSkip || parent.MidMove):
		tp.Action, tp.Skip = ActionSkip, SkipParentNotImported
	default:
		tp.Action = ActionCreate
		if parent != nil {
			tp.ParentGID = parent.GID
		}
	}

	written := tp.Action == ActionCreate || tp.Action == ActionUpdate
	if written && v.Truncated && (tp.Action == ActionCreate || tp.Change.Description != nil) {
		pp.Notes.Truncated++
	}
	if written && v.StartDropped {
		pp.Notes.StartDropped++
	}
}

// noteParent counts an imported task whose Asana parent no longer matches its row.
func (p *Plan) noteParent(pp *ProjectPlan, row *ExistingTask, parent *TaskPlan) {
	current := uint32(0)
	if row.ParentTaskID.Valid {
		current = uint32(row.ParentTaskID.Int32) //#nosec G115 -- generated NullInt32 of an INT UNSIGNED task id
	}
	var changed bool
	switch {
	case parent == nil:
		changed = current != 0
	case parent.Row != nil && !parent.MidMove:
		changed = current != parent.Row.ID
	default:
		changed = true
	}
	if changed {
		pp.Notes.ParentChanged++
	}
}

func sameDay(a, b sql.NullTime) bool {
	if a.Valid != b.Valid {
		return false
	}
	return !a.Valid || a.Time.Format(dateLayout) == b.Time.Format(dateLayout)
}

// mappedIDs is the account ids of the Asana-mapped people.
func (p *Plan) mappedIDs() map[uint32]bool {
	out := map[uint32]bool{}
	for e := range p.people.mapped() {
		if a, ok := p.Accounts[e]; ok {
			out[a.ID] = true
		}
	}
	return out
}

// asanaLabels is the project's Asana-derived label set: every tag and
// section label that exists.
func (p *Plan) asanaLabels(project int) map[uint32]bool {
	if set, ok := p.labelSets[project]; ok {
		return set
	}
	pp := p.Projects[project]
	set := map[uint32]bool{}
	for _, t := range p.snap.Tags {
		if id, ok := p.LabelID(LabelRef{Project: -1, Name: labelName(t.Name)}); ok {
			set[id] = true
		}
	}
	if ps := p.snap.Mapped[pp.Source.GID]; ps != nil {
		for _, s := range ps.Sections {
			if name := pp.conv.sectionLabel(s.Name); name != "" {
				if id, ok := p.LabelID(LabelRef{Project: project, Name: name}); ok {
					set[id] = true
				}
			}
		}
	}
	p.labelSets[project] = set
	return set
}

// diff compares a row with the Asana value; only Asana-mapped people and
// Asana-derived labels take part.
func (p *Plan) diff(pp *ProjectPlan, row *ExistingTask, v TaskValue) TaskChange {
	var c TaskChange
	if row.Title != v.Title {
		c.Title = &v.Title
	}
	if row.Description.String != v.Description {
		c.Description = &v.Description
	}
	if !sameDay(row.DueOn, v.DueOn) {
		due := v.DueOn
		c.DueOn = &due
	}
	if !sameDay(row.StartedOn, v.StartOn) {
		start := v.StartOn
		c.StartOn = &start
	}
	switch row.DerivedState {
	case generated.TasksDerivedStateCancelled:
		pp.Notes.CancelledKept++
	case generated.TasksDerivedStateDone:
		if !v.Completed {
			c.Transition = taskstate.TransitionReopen
		}
	default:
		if v.Completed {
			c.Transition = taskstate.TransitionComplete
		}
	}

	type actorKey struct {
		user uint32
		role generated.TaskActorsRole
	}
	mapped := p.mappedIDs()
	var have []actorKey
	haveSet := map[actorKey]bool{}
	for _, a := range p.existing.Actors[row.ID] {
		r := a.Role
		if !a.UserID.Valid || !mapped[uint32(a.UserID.Int32)] || //#nosec G115 -- generated NullInt32 of an INT UNSIGNED user id
			(r != generated.TaskActorsRoleAssignee && r != generated.TaskActorsRoleWatcher) {
			continue
		}
		k := actorKey{uint32(a.UserID.Int32), r} //#nosec G115 -- generated NullInt32 of an INT UNSIGNED user id
		have = append(have, k)
		haveSet[k] = true
	}
	var want []ActorRef
	if v.Assignee != "" {
		want = append(want, ActorRef{v.Assignee, generated.TaskActorsRoleAssignee})
	}
	for _, w := range v.Watchers {
		want = append(want, ActorRef{w, generated.TaskActorsRoleWatcher})
	}
	wantSet := map[actorKey]bool{}
	for _, w := range want {
		if a, ok := p.Accounts[w.Email]; ok {
			k := actorKey{a.ID, w.Role}
			wantSet[k] = true
			if haveSet[k] {
				continue
			}
		}
		c.AddActors = append(c.AddActors, w)
	}
	for _, k := range have {
		if !wantSet[k] {
			c.RemoveActors = append(c.RemoveActors, ActorRow{k.user, k.role})
		}
	}

	set := p.asanaLabels(pp.conv.project)
	current := map[uint32]bool{}
	for _, id := range p.existing.Labels[row.ID] {
		current[id] = true
	}
	wantIDs := map[uint32]bool{}
	for _, ref := range v.Labels {
		if id, ok := p.LabelID(ref); ok {
			wantIDs[id] = true
			if current[id] {
				continue
			}
		}
		c.AttachLabels = append(c.AttachLabels, ref)
	}
	for _, id := range p.existing.Labels[row.ID] {
		if set[id] && !wantIDs[id] {
			c.DetachLabels = append(c.DetachLabels, id)
		}
	}
	return c
}

// planTaskDeletions lists the mapped projects' rows that no placement keeps.
func (p *Plan) planTaskDeletions(byProject map[uint32]int) {
	for _, r := range p.existing.Tasks {
		j, mapped := byProject[r.ProjectID]
		if !mapped {
			continue
		}
		candidate := !p.Projects[j].listed[r.GID]
		if tp, placed := p.taskByGID[r.GID]; placed {
			if tp.Row == r || tp.Action == ActionSkip {
				continue
			}
			p.TaskDeletions = append(p.TaskDeletions, &TaskDeletion{Row: r, Project: j, Moved: true, Candidate: candidate, Delete: true})
			continue
		}
		p.TaskDeletions = append(p.TaskDeletions, &TaskDeletion{Row: r, Project: j, Candidate: candidate})
	}
}

// planChildren plans the comments and attachments of one task.
func (p *Plan) planChildren(tp *TaskPlan) {
	d := p.snap.Tasks[tp.GID]
	pp := p.Projects[tp.Project]
	importable := tp.Action != ActionSkip
	comments := map[string]*ExistingComment{}
	attachments := map[string]*ExistingAttachment{}
	if tp.Row != nil {
		for _, c := range p.existing.Comments[tp.Row.ID] {
			comments[c.GID] = c
		}
		for _, a := range p.existing.Attachments[tp.Row.ID] {
			attachments[a.GID] = a
		}
	}

	listed := map[string]bool{}
	for _, s := range d.Comments {
		listed[s.GID] = true
		cp := &CommentPlan{GID: s.GID, TaskGID: tp.GID, Project: tp.Project, CreatedAt: s.CreatedAt}
		p.Comments = append(p.Comments, cp)
		if !importable {
			cp.Action, cp.Skip = ActionSkip, SkipTaskNotImported
			continue
		}
		v := pp.conv.comment(s)
		cp.Author = v.Author
		ec := comments[s.GID]
		cp.Row = ec
		var truncated bool
		switch {
		case v.Markdown == "" && ec != nil && p.Sync:
			cp.Action, cp.Skip = ActionKeep, SkipEmptyBody
		case ec != nil && !p.Sync:
			cp.Action = ActionUnchanged
		case v.Markdown == "":
			cp.Action, cp.Skip = ActionSkip, SkipEmptyBody
		case ec == nil:
			cp.Action = ActionCreate
			cp.Body, truncated = v.body(v.Author == "")
		default:
			stored := ec.AuthorID.Valid && p.Importer.ID != 0 && uint32(ec.AuthorID.Int32) == p.Importer.ID //#nosec G115 -- generated NullInt32 of an INT UNSIGNED user id
			cp.Body, truncated = v.body(v.Author == "" && stored)
			cp.Action = ActionUnchanged
			if cp.Body != ec.Body {
				cp.Action = ActionUpdate
			}
		}
		if truncated && (cp.Action == ActionCreate || cp.Action == ActionUpdate) {
			pp.Notes.Truncated++
		}
	}
	if p.Sync && tp.Row != nil && importable {
		for _, ec := range p.existing.Comments[tp.Row.ID] {
			if !listed[ec.GID] {
				p.CommentDeletions = append(p.CommentDeletions, &CommentDeletion{Row: ec, TaskGID: tp.GID, Project: tp.Project})
			}
		}
	}

	inAsana := map[string]bool{}
	for _, a := range d.Attachments {
		if !hosted(a) {
			continue
		}
		inAsana[a.GID] = true
		ap := &AttachmentPlan{GID: a.GID, TaskGID: tp.GID, Project: tp.Project}
		p.Attachments = append(p.Attachments, ap)
		if !importable {
			ap.Action, ap.Skip = ActionSkip, SkipTaskNotImported
			continue
		}
		v := attachment(a)
		ap.Name, ap.ContentType, ap.Size, ap.Uploader = v.Name, v.ContentType, v.Size, tp.Value.CreatedBy
		ea := attachments[a.GID]
		switch {
		case ea != nil && ea.UploadedAt.Valid:
			ap.Action, ap.Row = ActionUnchanged, ea
		case v.Skip != "":
			ap.Action, ap.Skip = ActionSkip, v.Skip
		default:
			ap.Action, ap.Row = ActionCreate, ea
		}
	}
	if tp.Row != nil {
		for _, ea := range p.existing.Attachments[tp.Row.ID] {
			if !inAsana[ea.GID] {
				pp.Notes.AttachmentsRemovedInAsana++
			}
		}
	}
}

// labelNotes counts, per project, labels on its imported rows outside the
// Asana-derived set.
func (p *Plan) labelNotes() {
	left := map[int]map[uint32]bool{}
	for _, tp := range p.Tasks {
		if tp.Row == nil || tp.MidMove {
			continue
		}
		set := p.asanaLabels(tp.Project)
		for _, id := range p.existing.Labels[tp.Row.ID] {
			if !set[id] {
				if left[tp.Project] == nil {
					left[tp.Project] = map[uint32]bool{}
				}
				left[tp.Project][id] = true
			}
		}
	}
	for i, ids := range left {
		p.Projects[i].Notes.LabelsLeft = len(ids)
	}
}

// collectNewLabels lists the labels the planned writes need and do not find.
func (p *Plan) collectNewLabels() {
	type key struct {
		project int
		name    string
	}
	seen := map[key]bool{}
	need := func(refs []LabelRef) {
		for _, ref := range refs {
			k := key{ref.Project, nameKey(ref.Name)}
			if _, ok := p.LabelID(ref); ok || seen[k] {
				continue
			}
			seen[k] = true
			p.NewLabels = append(p.NewLabels, ref)
		}
	}
	for _, tp := range p.Tasks {
		switch tp.Action {
		case ActionCreate:
			need(tp.Value.Labels)
		case ActionUpdate:
			need(tp.Change.AttachLabels)
		}
	}
}
