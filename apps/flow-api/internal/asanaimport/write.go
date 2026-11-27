package asanaimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

const (
	// via tags the events this importer writes.
	via = "asana"
	// labelColor is the REST create default.
	labelColor = "#6b7280"
	stubNotes  = "created by importasana"
)

// Store is the object store attachments go to; *storage.Client is one.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType, sha256Hex string) error
	StatObject(ctx context.Context, key string) (int64, error)
}

// Downloader fetches an attachment's bytes; *asana.Client is one.
type Downloader interface {
	DownloadAttachment(ctx context.Context, gid string, w io.Writer) (int64, error)
}

// WriteDeps are what Write writes through. Storage and Source are nil
// interfaces when unavailable; their attachments then count as failed.
type WriteDeps struct {
	DB      *sql.DB
	Storage Store
	Source  Downloader
	// TempDir holds downloads while they upload; "" is the system default.
	TempDir string
}

// Counts is one §1.8 result line. Rejected is always 0 in flow.
type Counts struct {
	Imported, Updated, Duplicates, Deleted, Kept, Moved, Skipped, Failed, Rejected int
}

// ProjectResult is what a run did in one mapped project.
type ProjectResult struct {
	Created                                      bool
	MembersAdded, MembersExisting, MembersFailed int
	Tasks, Comments, Attachments, Dependencies   Counts
}

// WriteResult is what a run did. Projects follows Plan.Projects.
type WriteResult struct {
	Projects                        []ProjectResult
	UsersCreated, UsersFailed       int
	ProjectsCreated, ProjectsFailed int
	// Failures are the reasons, one per failed unit that has one.
	Failures []string
}

// Failed is every failed unit; a non-zero value means exit code 1.
func (r *WriteResult) Failed() int {
	n := r.UsersFailed + r.ProjectsFailed
	for _, p := range r.Projects {
		n += p.MembersFailed + p.Tasks.Failed + p.Comments.Failed + p.Attachments.Failed + p.Dependencies.Failed
	}
	return n
}

type taskRef struct {
	id  uint32
	pub types.PublicID
}

type writer struct {
	p        *Plan
	deps     WriteDeps
	q        *generated.Queries
	rec      *mutationlog.Recorder
	actor    mutationlog.Actor
	actorID  *int64
	res      *WriteResult
	accounts map[string]Account
	// projectIDs is the flow project of each plan project; 0 when its creation failed.
	projectIDs []uint32
	labels     map[labelKey]labelEntry
	labelPubs  map[uint32]types.PublicID
	tasks      map[string]taskRef // gid -> the row children attach to
}

// Write executes a prepared plan in the §4.5 order. A failed unit is
// counted and the run continues; a returned error means the run stopped
// early, and the result still counts what was written. The plan's
// Notes.CompletionGate are filled as a side effect.
func Write(ctx context.Context, p *Plan, deps WriteDeps) (*WriteResult, error) {
	switch {
	case deps.DB == nil:
		return nil, errors.New("asanaimport: no database")
	case !p.WorkspaceFound || p.Importer.ID == 0 || len(p.Errors) > 0:
		return nil, errors.New("asanaimport: the plan has validation errors")
	case len(p.Refusals()) > 0:
		return nil, errors.New("asanaimport: the deletion guard refused the plan")
	}
	importer := int64(p.Importer.ID)
	w := &writer{
		p: p, deps: deps,
		q:          generated.New(deps.DB),
		rec:        mutationlog.New(deps.DB, generated.New(deps.DB)),
		actor:      mutationlog.Actor{UserID: p.Importer.ID, WorkspaceID: p.Workspace.ID},
		actorID:    &importer,
		res:        &WriteResult{Projects: make([]ProjectResult, len(p.Projects))},
		accounts:   make(map[string]Account, len(p.Accounts)),
		projectIDs: make([]uint32, len(p.Projects)),
		tasks:      map[string]taskRef{},
	}
	for e, a := range p.Accounts {
		w.accounts[e] = a
	}
	ctx = eventbus.WithoutFanout(ctx)
	for _, step := range []func(context.Context) error{
		w.loadLabels, w.users, w.projects, w.createLabels,
		w.writeTasks, w.writeComments, w.writeAttachments, w.writeDependencies, w.writeDeletions,
	} {
		if err := step(ctx); err != nil {
			return w.res, err
		}
	}
	return w.res, nil
}

// fail counts a failed unit; a nil err adds no reason (the cause is reported elsewhere).
func (w *writer) fail(n *int, what string, err error) {
	*n++
	if err != nil {
		w.res.Failures = append(w.res.Failures, what+": "+err.Error())
	}
}

func (w *writer) inTx(ctx context.Context, label string, fn func(ctx context.Context, tx *dbretry.Tx) error) error {
	return dbretry.InTx(ctx, w.deps.DB, "asanaimport."+label, nil, fn)
}

// audit records the audit half after tx commits; the kit appended the event.
func (w *writer) audit(ctx context.Context, tx *dbretry.Tx, m mutationlog.Mutation) {
	tx.AfterCommit(func() { w.rec.RecordTxAudit(ctx, w.actor, m) })
}

// userID resolves an address; "" means the importer.
func (w *writer) userID(email string) (uint32, error) {
	if email == "" {
		return w.p.Importer.ID, nil
	}
	a, ok := w.accounts[email]
	if !ok {
		return 0, fmt.Errorf("account %q is not available", email)
	}
	return a.ID, nil
}

func nullInt32(id uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- INT UNSIGNED ids into the generated NullInt32 parameter
}
