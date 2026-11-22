package asanaimport

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

// maxUnguardedDeletes is how many tasks one project may lose whatever its
// size: small losses are accepted, as a soft delete can be undone.
const maxUnguardedDeletes = 10

// Guard refusal reasons, as the shared fixtures name them.
const (
	RefuseEmptySource = "empty-source"
	RefuseMassDelete  = "mass-delete"
)

// Action is what a run does with one source item.
type Action int

const (
	// ActionCreate writes a new row (imported).
	ActionCreate Action = iota + 1
	// ActionUpdate changes an existing row on a sync (updated).
	ActionUpdate
	// ActionUnchanged leaves an existing row as it is (duplicates).
	ActionUnchanged
	// ActionKeep leaves a row that a sync cannot convert (kept).
	ActionKeep
	// ActionSkip does not import the item (skipped); Skip says why.
	ActionSkip
)

// ActorRef is a task actor to add, by normalized address.
type ActorRef struct {
	Email string
	Role  generated.TaskActorsRole
}

// ActorRow is an existing task actor to remove.
type ActorRow struct {
	UserID uint32
	Role   generated.TaskActorsRole
}

// TaskChange is what a sync changes on an existing task. Nil pointers and
// empty slices leave the field alone.
type TaskChange struct {
	Title        *string
	Description  *string
	DueOn        *sql.NullTime
	StartOn      *sql.NullTime
	Transition   string // taskstate.TransitionComplete, TransitionReopen or ""
	AddActors    []ActorRef
	RemoveActors []ActorRow
	AttachLabels []LabelRef
	DetachLabels []uint32
}

func (c TaskChange) empty() bool {
	return c.Title == nil && c.Description == nil && c.DueOn == nil && c.StartOn == nil && c.Transition == "" &&
		len(c.AddActors) == 0 && len(c.RemoveActors) == 0 && len(c.AttachLabels) == 0 && len(c.DetachLabels) == 0
}

// TaskPlan is one placed Asana task. Plan.Tasks lists parents first.
type TaskPlan struct {
	GID     string
	Project int // index into Plan.Projects
	Action  Action
	Skip    string
	Subtype string
	// Row is the existing row compared or extended; nil on create.
	Row *ExistingTask
	// MidMove marks a run without -sync that found the task's row in
	// another mapped project; Row is that row and children attach to it.
	MidMove bool
	// ParentGID is the parent to link on create, placed in the same project.
	ParentGID string
	Value     TaskValue
	Change    TaskChange // ActionUpdate only
}

// TaskDeletion is a sourced row of a mapped project that a sync removes or keeps.
type TaskDeletion struct {
	Row     *ExistingTask
	Project int
	// Moved means the task was placed in another mapped project.
	Moved bool
	// Candidate means Asana no longer lists the task for this project; the
	// guard counts these.
	Candidate bool
	Probed    bool
	Probe     asana.ProbeResult
	// Delete is the decision: always for Moved, else by the probe.
	Delete bool
}

// CommentPlan is one Asana comment of a placed task.
type CommentPlan struct {
	GID     string
	TaskGID string
	Project int
	Action  Action
	Skip    string
	Row     *ExistingComment
	// Body is the text to store on create or update.
	Body string
	// Author is the resolved address; "" files the comment under the importer.
	Author    string
	CreatedAt time.Time
}

// CommentDeletion is a sourced comment that Asana no longer lists.
type CommentDeletion struct {
	Row     *ExistingComment
	TaskGID string
	Project int
	Probed  bool
	Probe   asana.ProbeResult
	Delete  bool
}

// AttachmentPlan is one Asana-hosted attachment of a placed task.
type AttachmentPlan struct {
	GID         string
	TaskGID     string
	Project     int
	Action      Action // ActionCreate, ActionUnchanged or ActionSkip
	Skip        string
	Name        string
	ContentType string
	Size        int64 // as Asana lists it
	// Uploader is the task's resolved creator; "" means the importer.
	Uploader string
	// Row is set on create when an unconfirmed row is refilled.
	Row *ExistingAttachment
}

// DependencyPlan is one Asana dependency: Blocker blocks Blocked.
type DependencyPlan struct {
	BlockerGID string
	BlockedGID string
	Project    int // the blocked task's project
	Action     Action
	Skip       string
}

// DependencyDeletion is a blocks edge between imported tasks that Asana no longer has.
type DependencyDeletion struct {
	Edge    generated.ListBlockingDependenciesForTasksRow
	Project int
}

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

// Plan is everything a run would write, resolved but not executed.
type Plan struct {
	Sync            bool
	Workspace       generated.FindWorkspaceBySlugRow
	WorkspaceFound  bool
	Location        *time.Location
	SourceWorkspace asana.Workspace
	// Importer is the importer's account; ID 0 when it does not exist.
	Importer Account
	// Accounts are the existing accounts by normalized address.
	Accounts            map[string]Account
	NewUsers            []MappingUser // normalized addresses, mapping order
	SignIn              SignIn
	Projects            []*ProjectPlan
	Tasks               []*TaskPlan
	TaskDeletions       []*TaskDeletion
	Comments            []*CommentPlan
	CommentDeletions    []*CommentDeletion
	Attachments         []*AttachmentPlan
	Dependencies        []*DependencyPlan
	DependencyDeletions []*DependencyDeletion
	// NewLabels are the labels a run creates, first use first.
	NewLabels []LabelRef
	Errors    []MappingError

	snap      *asana.Snapshot
	people    *people
	existing  *Existing
	labels    labelIndex
	placed    map[string]int
	taskByGID map[string]*TaskPlan
	labelSets map[int]map[uint32]bool
}

type labelKey struct {
	project uint32 // 0 for a workspace label
	key     string
}

// labelIndex finds enabled labels by scope and collation-equal name.
type labelIndex map[labelKey]uint32

func (l labelIndex) add(project uint32, name string, id uint32) {
	k := labelKey{project, nameKey(name)}
	if _, ok := l[k]; !ok {
		l[k] = id
	}
}

// LabelID finds the existing label a reference names.
func (p *Plan) LabelID(ref LabelRef) (uint32, bool) {
	var project uint32
	if ref.Project >= 0 {
		if project = p.Projects[ref.Project].ExistingID; project == 0 {
			return 0, false
		}
	}
	id, ok := p.labels[labelKey{project, nameKey(ref.Name)}]
	return id, ok
}

// Task returns the plan of a placed Asana task.
func (p *Plan) Task(gid string) *TaskPlan { return p.taskByGID[gid] }

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

type edge struct{ from, to string }

// reaches reports whether to is reachable from from.
func reaches(graph map[string][]string, from, to string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true
		}
		for _, m := range graph[n] {
			if !seen[m] {
				seen[m] = true
				stack = append(stack, m)
			}
		}
	}
	return false
}

// planDependencies aligns blocks edges between imported tasks with Asana.
func (p *Plan) planDependencies() {
	rowGID := map[uint32]string{}
	for _, tp := range p.Tasks {
		if tp.Row != nil && tp.Action != ActionSkip {
			rowGID[tp.Row.ID] = tp.GID
		}
	}
	existing := map[edge]bool{}
	var existingOrder []edge
	var existingRows []generated.ListBlockingDependenciesForTasksRow
	for _, e := range p.existing.Blocks {
		f, ok1 := rowGID[e.FromTaskID]
		t, ok2 := rowGID[e.ToTaskID]
		if ok1 && ok2 && !existing[edge{f, t}] {
			existing[edge{f, t}] = true
			existingOrder = append(existingOrder, edge{f, t})
			existingRows = append(existingRows, e)
		}
	}

	desired := map[edge]bool{}
	var creates []*DependencyPlan
	for _, tp := range p.Tasks {
		seen := map[string]bool{}
		for _, dep := range p.snap.Tasks[tp.GID].Task.Dependencies {
			if seen[dep.GID] {
				continue
			}
			seen[dep.GID] = true
			dp := &DependencyPlan{BlockerGID: dep.GID, BlockedGID: tp.GID, Project: tp.Project}
			p.Dependencies = append(p.Dependencies, dp)
			blocker := p.taskByGID[dep.GID]
			if tp.Action == ActionSkip || blocker == nil || blocker.Action == ActionSkip {
				dp.Action, dp.Skip = ActionSkip, SkipOutsideMapping
				continue
			}
			k := edge{dep.GID, tp.GID}
			desired[k] = true
			if existing[k] {
				dp.Action = ActionUnchanged
				continue
			}
			creates = append(creates, dp)
		}
	}

	graph := map[string][]string{}
	for _, k := range existingOrder {
		if desired[k] || !p.Sync {
			graph[k.from] = append(graph[k.from], k.to)
		}
	}
	sorted := append([]*DependencyPlan(nil), creates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.BlockerGID != b.BlockerGID {
			return gidLess(a.BlockerGID, b.BlockerGID)
		}
		return gidLess(a.BlockedGID, b.BlockedGID)
	})
	for _, dp := range sorted {
		if reaches(graph, dp.BlockedGID, dp.BlockerGID) {
			dp.Action, dp.Skip = ActionSkip, SkipCycle
			continue
		}
		dp.Action = ActionCreate
		graph[dp.BlockerGID] = append(graph[dp.BlockerGID], dp.BlockedGID)
	}

	if p.Sync {
		for i, k := range existingOrder {
			if !desired[k] {
				p.DependencyDeletions = append(p.DependencyDeletions, &DependencyDeletion{Edge: existingRows[i], Project: p.taskByGID[k.to].Project})
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
