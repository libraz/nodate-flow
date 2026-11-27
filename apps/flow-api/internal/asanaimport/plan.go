package asanaimport

import (
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
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
type labelIndex map[labelKey]labelEntry

// labelEntry is a label's internal id and its public id.
type labelEntry struct {
	id  uint32
	pub types.PublicID
}

func (l labelIndex) add(project uint32, name string, e labelEntry) {
	k := labelKey{project, nameKey(name)}
	if _, ok := l[k]; !ok {
		l[k] = e
	}
}

// label finds the existing label a reference names.
func (p *Plan) label(ref LabelRef) (labelEntry, bool) {
	var project uint32
	if ref.Project >= 0 {
		if project = p.Projects[ref.Project].ExistingID; project == 0 {
			return labelEntry{}, false
		}
	}
	e, ok := p.labels[labelKey{project, nameKey(ref.Name)}]
	return e, ok
}

// LabelID finds the internal id of the existing label a reference names.
func (p *Plan) LabelID(ref LabelRef) (uint32, bool) {
	e, ok := p.label(ref)
	return e.id, ok
}

// Task returns the plan of a placed Asana task.
func (p *Plan) Task(gid string) *TaskPlan { return p.taskByGID[gid] }
