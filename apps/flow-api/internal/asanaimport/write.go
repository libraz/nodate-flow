package asanaimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/attachkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/labelkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/projectkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskactors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdeps"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
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

func labelScope(n sql.NullInt32) uint32 {
	if !n.Valid {
		return 0
	}
	return uint32(n.Int32) //#nosec G115 -- generated NullInt32 of an INT UNSIGNED project id
}

// loadLabels indexes the workspace's enabled labels with their public ids.
func (w *writer) loadLabels(ctx context.Context) error {
	rows, err := w.q.ListLabelsForImport(ctx, w.p.Workspace.ID)
	if err != nil {
		return fmt.Errorf("read labels: %w", err)
	}
	w.labels = map[labelKey]labelEntry{}
	w.labelPubs = map[uint32]types.PublicID{}
	for _, r := range rows {
		w.labels[labelKey{labelScope(r.ProjectID), nameKey(r.Name)}] = labelEntry{r.ID, r.PublicID}
		w.labelPubs[r.ID] = r.PublicID
	}
	return nil
}

// labelKeyOf scopes a reference; false when its project was not created.
func (w *writer) labelKeyOf(ref LabelRef) (labelKey, bool) {
	var project uint32
	if ref.Project >= 0 {
		if project = w.projectIDs[ref.Project]; project == 0 {
			return labelKey{}, false
		}
	}
	return labelKey{project, nameKey(ref.Name)}, true
}

func (w *writer) label(ref LabelRef) (labelEntry, error) {
	k, ok := w.labelKeyOf(ref)
	if ok {
		if e, found := w.labels[k]; found {
			return e, nil
		}
	}
	return labelEntry{}, fmt.Errorf("label %q is not available", ref.Name)
}

// users creates the mapping's new accounts, one transaction each.
func (w *writer) users(ctx context.Context) error {
	ws := w.p.Workspace
	for _, u := range w.p.NewUsers {
		if err := ctx.Err(); err != nil {
			return err
		}
		var id uint32
		pub := types.New()
		err := w.inTx(ctx, "CreateUser", func(ctx context.Context, tx *dbretry.Tx) error {
			q := generated.New(tx)
			raw, err := q.CreateStubUser(ctx, generated.CreateStubUserParams{
				PublicID: pub, Email: u.Email, DisplayName: u.Name, Locale: "en",
				Timezone: ws.Timezone, Country: ws.Country, ThemePreference: generated.UsersThemePreferenceSystem,
			})
			if err != nil {
				return err
			}
			id = uint32(raw) //#nosec G115 -- LastInsertId of users.id, fits uint32 within realistic deployments
			if _, err := memberkit.AddWorkspaceMember(ctx, tx, memberkit.AddWorkspaceMemberArgs{
				WorkspaceID: ws.ID, UserID: id, Role: memberkit.RoleMember,
				InvitedByUserID: w.p.Importer.ID, EnsurePersonalCalendar: true,
			}); err != nil {
				return err
			}
			// An open deployment gets no row: one would restrict sign-in for everyone.
			if !w.p.SignIn.Restricted() {
				return nil
			}
			return q.UpsertOauthSigninAllowlistEntry(ctx, generated.UpsertOauthSigninAllowlistEntryParams{
				PublicID: types.New(), AddedByUserID: nullInt32(w.p.Importer.ID),
				EntryKind: generated.OauthSigninAllowlistEntryKindEmail, EntryValue: u.Email,
				Notes: sql.NullString{String: stubNotes, Valid: true},
			})
		})
		if err != nil {
			w.fail(&w.res.UsersFailed, fmt.Sprintf("user %q", u.Email), err)
			continue
		}
		w.res.UsersCreated++
		w.accounts[u.Email] = Account{ID: id, PublicID: pub, Email: u.Email, Name: u.Name, Enabled: true}
	}
	return nil
}

// projects creates or reuses each mapped project and adds its members.
func (w *writer) projects(ctx context.Context) error {
	ws := w.p.Workspace
	for i, pp := range w.p.Projects {
		if err := ctx.Err(); err != nil {
			return err
		}
		pr := &w.res.Projects[i]
		w.projectIDs[i] = pp.ExistingID
		if pp.ExistingID == 0 {
			var created projectkit.Project
			err := w.inTx(ctx, "CreateProject", func(ctx context.Context, tx *dbretry.Tx) error {
				var err error
				created, err = projectkit.Create(ctx, tx, projectkit.CreateArgs{
					WorkspaceID: ws.ID, Slug: pp.Slug,
					Identifier: sql.NullString{String: pp.Identifier, Valid: pp.Identifier != ""},
					Name:       pp.Name, LeadUserID: w.p.Importer.ID,
				})
				return err
			})
			if err != nil {
				w.fail(&w.res.ProjectsFailed, fmt.Sprintf("project %q", pp.Name), err)
				continue
			}
			audit.New(w.q).Record(ctx, audit.Entry{
				Action: "project.create", ActorID: w.p.Importer.ID, WorkspaceID: ws.ID,
				ResourceType: "project", ResourceID: created.PublicID.String(),
				Metadata: map[string]any{"slug": pp.Slug, "name": pp.Name},
			})
			w.projectIDs[i] = created.ID
			pr.Created = true
			w.res.ProjectsCreated++
		}
		for _, m := range pp.Members {
			if m.Existing {
				pr.MembersExisting++
				continue
			}
			what := fmt.Sprintf("project %q member %q", pp.Name, m.Email)
			uid, err := w.userID(m.Email)
			if err != nil {
				w.fail(&pr.MembersFailed, what, err)
				continue
			}
			var added bool
			err = w.inTx(ctx, "AddProjectMember", func(ctx context.Context, tx *dbretry.Tx) error {
				var err error
				added, err = projectkit.AddMember(ctx, tx, projectkit.MemberArgs{
					WorkspaceID: ws.ID, ProjectID: w.projectIDs[i], UserID: uid,
					Role: m.Role, PublicID: types.New(), AddedAt: time.Now(),
				})
				return err
			})
			switch {
			case err != nil:
				w.fail(&pr.MembersFailed, what, err)
			case added:
				pr.MembersAdded++
			default:
				pr.MembersExisting++
			}
		}
	}
	return nil
}

// createLabels creates the labels the plan does not find. A name taken
// since the plan was read is looked up again rather than counted.
func (w *writer) createLabels(ctx context.Context) error {
	for _, ref := range w.p.NewLabels {
		if err := ctx.Err(); err != nil {
			return err
		}
		k, ok := w.labelKeyOf(ref)
		if !ok {
			continue
		}
		if _, found := w.labels[k]; found {
			continue
		}
		var l labelkit.Label
		err := w.inTx(ctx, "CreateLabel", func(ctx context.Context, tx *dbretry.Tx) error {
			var err error
			l, err = labelkit.Create(ctx, tx, labelkit.CreateArgs{
				WorkspaceID: w.p.Workspace.ID,
				ProjectID:   sql.NullInt64{Int64: int64(k.project), Valid: k.project != 0},
				Name:        ref.Name, Color: labelColor, Actor: w.actor, Via: via,
			})
			if err != nil {
				return err
			}
			w.audit(ctx, tx, mutationlog.Mutation{
				AuditAction: "label.create", ResourceType: "label", ResourceID: l.PublicID.String(),
				Payload:  map[string]any{"labelId": l.PublicID.String(), "name": ref.Name},
				CallSite: "asanaimport.Write (labelkit.Create appended the event)",
			})
			return nil
		})
		switch {
		case errors.Is(err, labelkit.ErrNameTaken):
			if err := w.loadLabels(ctx); err != nil {
				return err
			}
		case err != nil:
			// The tasks that need it fail on their own.
			w.res.Failures = append(w.res.Failures, fmt.Sprintf("label %q: %v", ref.Name, err))
		default:
			w.labels[k] = labelEntry{l.ID, l.PublicID}
			w.labelPubs[l.ID] = l.PublicID
		}
	}
	return nil
}

// writeTasks writes each task in one transaction, parents first.
func (w *writer) writeTasks(ctx context.Context) error {
	for _, tp := range w.p.Tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[tp.Project].Tasks
		if tp.Row != nil && tp.Action != ActionSkip {
			w.tasks[tp.GID] = taskRef{tp.Row.ID, tp.Row.PublicID}
		}
		switch tp.Action {
		case ActionSkip:
			c.Skipped++
		case ActionKeep:
			c.Kept++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate:
			w.createTask(ctx, tp, c)
		case ActionUpdate:
			w.updateTask(ctx, tp, c)
		}
	}
	for i := range w.p.Projects {
		w.res.Projects[i].Tasks.Kept += w.p.TaskSync(i).Orphaned
	}
	return nil
}

func (w *writer) createTask(ctx context.Context, tp *TaskPlan, c *Counts) {
	what := "task " + tp.GID
	projectID := w.projectIDs[tp.Project]
	if projectID == 0 {
		w.fail(&c.Failed, what, nil)
		return
	}
	var parent sql.NullInt32
	if tp.ParentGID != "" {
		ref, ok := w.tasks[tp.ParentGID]
		if !ok {
			c.Skipped++ // parent not imported
			return
		}
		parent = nullInt32(ref.id)
	}
	v := tp.Value
	title, err := taskrules.NewTitle(v.Title)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	creator, err := w.userID(v.CreatedBy)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	var actors []taskcreate.Actor
	want := make([]ActorRef, 0, 1+len(v.Watchers))
	if v.Assignee != "" {
		want = append(want, ActorRef{v.Assignee, generated.TaskActorsRoleAssignee})
	}
	for _, e := range v.Watchers {
		want = append(want, ActorRef{e, generated.TaskActorsRoleWatcher})
	}
	for _, a := range want {
		uid, err := w.userID(a.Email)
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		actors = append(actors, taskcreate.Actor{UserID: uid, Role: a.Role})
	}
	labels := make([]labelEntry, 0, len(v.Labels))
	for _, ref := range v.Labels {
		l, err := w.label(ref)
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		labels = append(labels, l)
	}

	var (
		ref  taskRef
		gate int
	)
	err = w.inTx(ctx, "CreateTask", func(ctx context.Context, tx *dbretry.Tx) error {
		gate = 0
		created, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(creator).WithActors(actors...), taskcreate.Args{
			WorkspaceID: w.p.Workspace.ID, ProjectID: projectID, ParentTaskID: parent, Title: title,
			Description: sql.NullString{String: v.Description, Valid: v.Description != ""},
			DueOn:       v.DueOn, StartedOn: v.StartOn, CreatedAt: v.CreatedAt, SourceUID: SourceUID(tp.GID),
		})
		if err != nil {
			return err
		}
		taskID := created.ID
		ref = taskRef{uint32(created.ID), created.PublicID} //#nosec G115 -- tasks.id fits uint32 within realistic deployments
		if err := w.rec.RecordInTx(ctx, tx, w.actor, mutationlog.Mutation{
			EventType: eventbus.TaskCreated, AuditAction: "task.create",
			ResourceType: "task", ResourceID: created.PublicID.String(), TaskID: &taskID,
			Payload:  map[string]any{"taskId": created.PublicID.String(), "title": title.String(), "source": via},
			CallSite: "asanaimport.CreateTask",
		}); err != nil {
			return err
		}
		for _, l := range labels {
			if err := w.attachLabel(ctx, tx, ref, l); err != nil {
				return err
			}
		}
		if v.Completed {
			gated, err := w.transition(ctx, tx, ref, taskstate.TransitionComplete)
			if err != nil {
				return err
			}
			if gated {
				gate++
			}
		}
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	w.tasks[tp.GID] = ref
	w.p.Projects[tp.Project].Notes.CompletionGate += gate
	c.Imported++
}

func (w *writer) updateTask(ctx context.Context, tp *TaskPlan, c *Counts) {
	what := "task " + tp.GID
	ch := tp.Change
	ref := taskRef{tp.Row.ID, tp.Row.PublicID}
	adds := make([]Account, len(ch.AddActors))
	for i, a := range ch.AddActors {
		acc, ok := w.accounts[a.Email]
		if !ok {
			w.fail(&c.Failed, what, fmt.Errorf("account %q is not available", a.Email))
			return
		}
		adds[i] = acc
	}
	attach := make([]labelEntry, len(ch.AttachLabels))
	for i, lr := range ch.AttachLabels {
		l, err := w.label(lr)
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		attach[i] = l
	}
	patch := taskupdate.Patch{Title: ch.Title, Description: ch.Description, DueOn: ch.DueOn, StartOn: ch.StartOn}
	patched := patch.Title != nil || patch.Description != nil || patch.DueOn != nil || patch.StartOn != nil

	var gate int
	err := w.inTx(ctx, "UpdateTask", func(ctx context.Context, tx *dbretry.Tx) error {
		gate = 0
		if patched {
			r, err := taskupdate.Apply(ctx, tx, taskupdate.Args{
				WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, PublicID: ref.pub,
				Patch: patch, Actor: w.actor, Via: via,
			})
			if err != nil {
				return err
			}
			w.audit(ctx, tx, mutationlog.Mutation{
				AuditAction: "task.update", ResourceType: "task", ResourceID: ref.pub.String(), Payload: r.Payload,
				CallSite: "asanaimport.Write (taskupdate.Apply appended the event)",
			})
		}
		// Removals first: a changed assignee frees the role before it is given.
		for _, rm := range ch.RemoveActors {
			if err := w.removeActor(ctx, tx, ref, rm); err != nil {
				return err
			}
		}
		for i, a := range adds {
			if err := w.addActor(ctx, tx, ref, a, ch.AddActors[i].Role); err != nil {
				return err
			}
		}
		for _, id := range ch.DetachLabels {
			if err := w.detachLabel(ctx, tx, ref, id); err != nil {
				return err
			}
		}
		for _, l := range attach {
			if err := w.attachLabel(ctx, tx, ref, l); err != nil {
				return err
			}
		}
		if ch.Transition != "" {
			gated, err := w.transition(ctx, tx, ref, ch.Transition)
			if err != nil {
				return err
			}
			if gated {
				gate++
			}
		}
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	w.p.Projects[tp.Project].Notes.CompletionGate += gate
	// A gated completion that was the only difference changed nothing.
	if gate > 0 && !patched && len(ch.RemoveActors) == 0 && len(adds) == 0 && len(ch.DetachLabels) == 0 && len(attach) == 0 {
		c.Duplicates++
		return
	}
	c.Updated++
}

// transition applies a completion change; gated reports the D8 refusal,
// which leaves tx usable.
func (w *writer) transition(ctx context.Context, tx *dbretry.Tx, ref taskRef, verb string) (gated bool, err error) {
	r, spec, err := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
		WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, PublicID: ref.pub,
		Transition: verb, ActorUserID: w.actorID, Via: via,
	})
	if err != nil {
		return false, err
	}
	if spec == apierrors.WsTaskCompletionNeedsApproval {
		return true, nil
	}
	if spec != nil {
		return false, fmt.Errorf("transition %s refused: %s", verb, spec.Code)
	}
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.transition", ResourceType: "task", ResourceID: ref.pub.String(),
		Payload:  map[string]any{"transition": verb, "fromState": string(r.FromState), "toState": string(r.ToState)},
		CallSite: "asanaimport.Write (taskstate.ApplyTransitionTx appended the event)",
	})
	return false, nil
}

func (w *writer) addActor(ctx context.Context, tx *dbretry.Tx, ref taskRef, a Account, role generated.TaskActorsRole) error {
	pub := types.New()
	err := taskactors.Add(ctx, tx, taskactors.Args{
		Actor: w.actor, TaskID: ref.id, TaskPublicID: ref.pub.String(),
		UserID: a.ID, UserPublicID: a.PublicID.String(), Role: role, PublicID: pub,
	})
	if errors.Is(err, taskactors.ErrAlreadyActor) {
		return nil
	}
	if err != nil {
		return err
	}
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.actor.add", ResourceType: "task_actor", ResourceID: pub.String(),
		Payload: map[string]any{
			"taskId": ref.pub.String(), "actorId": pub.String(), "userId": a.PublicID.String(), "role": string(role),
		},
		CallSite: "asanaimport.Write (taskactors.Add appended the event)",
	})
	return nil
}

func (w *writer) removeActor(ctx context.Context, tx *dbretry.Tx, ref taskRef, rm ActorRow) error {
	pub, err := generated.New(tx).FindActiveTaskActor(ctx, generated.FindActiveTaskActorParams{
		WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, UserID: nullInt32(rm.UserID), Role: rm.Role,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = taskactors.Remove(ctx, tx, taskactors.Args{
		Actor: w.actor, TaskID: ref.id, TaskPublicID: ref.pub.String(), ActorPublicID: pub, Mutations: w.rec,
	})
	if errors.Is(err, taskactors.ErrNotActor) {
		return nil
	}
	if err != nil {
		return err
	}
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.actor.remove", ResourceType: "task_actor", ResourceID: pub.String(),
		Payload:  map[string]any{"taskId": ref.pub.String(), "actorId": pub.String()},
		CallSite: "asanaimport.Write (taskactors.Remove appended the event)",
	})
	return nil
}

func (w *writer) attachLabel(ctx context.Context, tx *dbretry.Tx, ref taskRef, l labelEntry) error {
	err := labelkit.Attach(ctx, tx, labelkit.AttachArgs{TaskID: ref.id, LabelID: l.id, LabelPublicID: l.pub, Actor: w.actor, Via: via})
	if errors.Is(err, labelkit.ErrAlreadyAttached) {
		return nil
	}
	if err != nil {
		return err
	}
	taskID := int64(ref.id)
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.label.add", ResourceType: "task_label", ResourceID: l.pub.String(), TaskID: &taskID,
		Payload:  map[string]any{"labelId": l.pub.String()},
		CallSite: "asanaimport.Write (labelkit.Attach appended the event)",
	})
	return nil
}

func (w *writer) detachLabel(ctx context.Context, tx *dbretry.Tx, ref taskRef, id uint32) error {
	pub, ok := w.labelPubs[id]
	if !ok {
		return errors.New("a label to detach is no longer enabled")
	}
	err := labelkit.Detach(ctx, tx, labelkit.AttachArgs{TaskID: ref.id, LabelID: id, LabelPublicID: pub, Actor: w.actor, Via: via})
	if errors.Is(err, labelkit.ErrNotAttached) {
		return nil
	}
	if err != nil {
		return err
	}
	taskID := int64(ref.id)
	w.audit(ctx, tx, mutationlog.Mutation{
		AuditAction: "task.label.remove", ResourceType: "task_label", ResourceID: pub.String(), TaskID: &taskID,
		Payload:  map[string]any{"labelId": pub.String()},
		CallSite: "asanaimport.Write (labelkit.Detach appended the event)",
	})
	return nil
}

// writeComments writes each comment in one transaction.
func (w *writer) writeComments(ctx context.Context) error {
	for _, cp := range w.p.Comments {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[cp.Project].Comments
		switch cp.Action {
		case ActionSkip:
			c.Skipped++
		case ActionKeep:
			c.Kept++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate, ActionUpdate:
			ref, ok := w.tasks[cp.TaskGID]
			if !ok {
				c.Skipped++ // task not imported
				continue
			}
			if cp.Action == ActionCreate {
				w.createComment(ctx, cp, ref, c)
			} else {
				w.editComment(ctx, cp, ref, c)
			}
		}
	}
	return nil
}

func (w *writer) createComment(ctx context.Context, cp *CommentPlan, ref taskRef, c *Counts) {
	what := "comment " + cp.GID
	author, err := w.userID(cp.Author)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	err = w.inTx(ctx, "WriteComment", func(ctx context.Context, tx *dbretry.Tx) error {
		written, err := taskservice.WriteComment(ctx, tx, taskservice.WriteArgs{
			Actor: w.actor, TaskID: ref.id, TaskPublicID: ref.pub,
			AuthorUserID: sql.NullInt64{Int64: int64(author), Valid: true},
			Body:         cp.Body, CreatedAt: cp.CreatedAt, SourceUID: SourceUID(cp.GID),
		})
		if err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "comment.create", ResourceType: "comment", ResourceID: written.PublicID.String(),
			Payload:  map[string]any{"taskId": ref.pub.String(), "commentId": written.PublicID.String()},
			CallSite: "asanaimport.Write (taskservice.WriteComment appended the event)",
		})
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	c.Imported++
}

func (w *writer) editComment(ctx context.Context, cp *CommentPlan, ref taskRef, c *Counts) {
	what := "comment " + cp.GID
	row := cp.Row
	if !row.AuthorID.Valid {
		w.fail(&c.Failed, what, errors.New("the stored comment has no human author"))
		return
	}
	err := w.inTx(ctx, "EditComment", func(ctx context.Context, tx *dbretry.Tx) error {
		if err := taskservice.EditComment(ctx, tx, taskservice.EditArgs{
			Actor: w.actor, CommentID: row.ID, CommentPublicID: row.PublicID,
			TaskID: ref.id, TaskPublicID: ref.pub,
			AuthorUserID: uint32(row.AuthorID.Int32), //#nosec G115 -- generated NullInt32 of an INT UNSIGNED user id
			Body:         cp.Body,
		}); err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "comment.update", ResourceType: "comment", ResourceID: row.PublicID.String(),
			Payload:  map[string]any{"taskId": ref.pub.String(), "commentId": row.PublicID.String()},
			CallSite: "asanaimport.Write (taskservice.EditComment appended the event)",
		})
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	c.Updated++
}

// errTooLarge stops a download past the upload ceiling.
var errTooLarge = errors.New("attachment exceeds the upload limit")

// limitWriter refuses bytes past left.
type limitWriter struct {
	w    io.Writer
	left int64
}

func (l *limitWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > l.left {
		return 0, errTooLarge
	}
	l.left -= int64(len(b))
	return l.w.Write(b)
}

// writeAttachments copies each missing attachment: download, reserve,
// upload, confirm (§4.6).
func (w *writer) writeAttachments(ctx context.Context) error {
	for _, ap := range w.p.Attachments {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[ap.Project].Attachments
		switch ap.Action {
		case ActionSkip:
			c.Skipped++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate:
			ref, ok := w.tasks[ap.TaskGID]
			if !ok {
				c.Skipped++ // task not imported
				continue
			}
			w.copyAttachment(ctx, ap, ref, c)
		}
	}
	return nil
}

func (w *writer) copyAttachment(ctx context.Context, ap *AttachmentPlan, ref taskRef, c *Counts) {
	what := "attachment " + ap.GID
	if w.deps.Storage == nil || w.deps.Source == nil {
		w.fail(&c.Failed, what, errors.New("storage or the Asana download is not configured"))
		return
	}
	uploader, err := w.userID(ap.Uploader)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	f, err := os.CreateTemp(w.deps.TempDir, "asana-attachment-*")
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	h := sha256.New()
	n, err := w.deps.Source.DownloadAttachment(ctx, ap.GID, &limitWriter{w: io.MultiWriter(f, h), left: storage.MaxUploadSize})
	if errors.Is(err, errTooLarge) {
		c.Skipped++ // too large
		return
	}
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	sum := h.Sum(nil)
	put := func(key, contentType string) error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return w.deps.Storage.Put(ctx, key, f, n, contentType, hex.EncodeToString(sum))
	}
	size := uint64(n) //#nosec G115 -- a byte count, non-negative

	if ap.Row != nil {
		// A reserved row whose object never confirmed: fill the object it names.
		obj, err := w.q.FindStorageObjectByID(ctx, ap.Row.StorageObjectID)
		if err == nil && !bytes.Equal(obj.Sha256, sum) {
			err = errors.New("the file in Asana no longer matches the reserved upload")
		}
		if err == nil {
			err = put(obj.StorageKey, obj.ContentType)
		}
		if err == nil {
			err = attachkit.MarkUploaded(ctx, w.deps.DB, attachkit.MarkArgs{
				WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, StorageObjectID: obj.ID, Size: size,
			})
		}
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		c.Imported++
		return
	}

	res, err := attachkit.Reserve(ctx, w.deps.DB, attachkit.Args{
		WorkspaceID: w.p.Workspace.ID, WorkspacePublicID: w.p.Workspace.PublicID,
		TaskID: ref.id, TaskPublicID: ref.pub,
		Filename: ap.Name, ContentType: ap.ContentType, Size: size, SHA256: sum,
		SourceUID: SourceUID(ap.GID), UploaderUserID: uploader, Actor: w.actor, Store: w.deps.Storage,
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	taskID := int64(ref.id)
	w.rec.RecordTxAudit(ctx, w.actor, mutationlog.Mutation{
		AuditAction: "attachment.create", ResourceType: "attachment",
		ResourceID: res.AttachmentPublicID.String(), TaskID: &taskID,
		Payload: map[string]any{
			"taskId": ref.pub.String(), "attachmentId": res.AttachmentPublicID.String(), "filename": ap.Name,
		},
		CallSite: "asanaimport.Write (attachkit.Reserve appended the event)",
	})
	// The row stays unconfirmed on a failure here; the next run refills it.
	if !res.AlreadyUploaded {
		if err := put(res.StorageKey, ap.ContentType); err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
	}
	if err := attachkit.MarkUploaded(ctx, w.deps.DB, attachkit.MarkArgs{
		WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, StorageObjectID: res.StorageObjectID, Size: size,
	}); err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	c.Imported++
}

// writeDependencies removes the edges Asana dropped, then adds the new
// ones: the plan's cycle check assumed the removals.
func (w *writer) writeDependencies(ctx context.Context) error {
	pubs := map[uint32]types.PublicID{}
	for _, t := range w.p.existing.Tasks {
		pubs[t.ID] = t.PublicID
	}
	for _, dd := range w.p.DependencyDeletions {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[dd.Project].Dependencies
		e := dd.Edge
		err := w.inTx(ctx, "RemoveDependency", func(ctx context.Context, tx *dbretry.Tx) error {
			if err := taskdeps.Remove(ctx, tx, taskdeps.RemoveArgs{
				WorkspaceID: w.p.Workspace.ID, FromTaskID: e.FromTaskID, DependencyID: e.PublicID,
				ActorUserID: w.actorID, FromTaskPublicID: pubs[e.FromTaskID].String(),
			}); err != nil {
				return err
			}
			w.audit(ctx, tx, mutationlog.Mutation{
				AuditAction: "task.dependency.remove", ResourceType: "task_dependency", ResourceID: e.PublicID.String(),
				CallSite: "asanaimport.Write (taskdeps.Remove appended the event)",
			})
			return nil
		})
		if err != nil && !errors.Is(err, taskdeps.ErrNotFound) {
			w.fail(&c.Failed, "dependency "+e.PublicID.String(), err)
			continue
		}
		c.Deleted++
	}
	for _, dp := range w.p.Dependencies {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[dp.Project].Dependencies
		switch dp.Action {
		case ActionSkip:
			c.Skipped++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate:
			w.addDependency(ctx, dp, c)
		}
	}
	return nil
}

func (w *writer) addDependency(ctx context.Context, dp *DependencyPlan, c *Counts) {
	from, ok1 := w.tasks[dp.BlockerGID]
	to, ok2 := w.tasks[dp.BlockedGID]
	if !ok1 || !ok2 {
		c.Skipped++ // an end was not imported
		return
	}
	err := w.inTx(ctx, "AddDependency", func(ctx context.Context, tx *dbretry.Tx) error {
		pub, err := taskdeps.Add(ctx, tx, taskdeps.Args{
			WorkspaceID: w.p.Workspace.ID, FromTaskID: from.id, ToTaskID: to.id,
			Kind: generated.TaskDependenciesKindBlocks, ActorUserID: w.actorID,
			FromTaskPublicID: from.pub.String(), ToTaskPublicID: to.pub.String(), Via: via,
		})
		if err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "task.dependency.add", ResourceType: "task_dependency", ResourceID: pub.String(),
			CallSite: "asanaimport.Write (taskdeps.Add appended the event)",
		})
		return nil
	})
	switch {
	case errors.Is(err, taskdeps.ErrDuplicate):
		c.Duplicates++
	case errors.Is(err, taskdeps.ErrCycle):
		c.Skipped++ // cycle
	case err != nil:
		w.fail(&c.Failed, "dependency "+dp.BlockerGID+" -> "+dp.BlockedGID, err)
	default:
		c.Imported++
	}
}

// writeDeletions applies a sync's task and comment removals.
func (w *writer) writeDeletions(ctx context.Context) error {
	for _, d := range w.p.TaskDeletions {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[d.Project].Tasks
		switch {
		case d.Moved:
			// The old row goes only once the new placement holds a row.
			if ref, ok := w.tasks[d.Row.GID]; !ok || ref.id == d.Row.ID {
				c.Kept++
				continue
			}
			if w.deleteTask(ctx, d.Row, c) {
				c.Moved++
			}
		case d.Delete:
			if w.deleteTask(ctx, d.Row, c) {
				c.Deleted++
			}
		case d.Probed:
			c.Kept++
		}
	}
	for _, d := range w.p.CommentDeletions {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[d.Project].Comments
		switch {
		case d.Delete:
			w.deleteComment(ctx, d, c)
		case d.Probed:
			c.Kept++
		}
	}
	return nil
}

func (w *writer) deleteTask(ctx context.Context, row *ExistingTask, c *Counts) bool {
	err := w.inTx(ctx, "DeleteTask", func(ctx context.Context, tx *dbretry.Tx) error {
		deleted, err := itemkit.DeleteTask(ctx, tx, w.p.Workspace.ID, row.ID, w.p.Importer.ID)
		if err != nil {
			return err
		}
		// A row already gone changed nothing and records nothing.
		if deleted {
			w.audit(ctx, tx, mutationlog.Mutation{
				AuditAction: "task.delete", ResourceType: "task", ResourceID: row.PublicID.String(),
				CallSite: "asanaimport.Write (itemkit.DeleteTask appended the event)",
			})
		}
		return nil
	})
	if err != nil {
		w.fail(&c.Failed, "task "+row.GID, err)
		return false
	}
	return true
}

func (w *writer) deleteComment(ctx context.Context, d *CommentDeletion, c *Counts) {
	ref := w.tasks[d.TaskGID]
	row := d.Row
	err := w.inTx(ctx, "DeleteComment", func(ctx context.Context, tx *dbretry.Tx) error {
		if err := taskservice.DisableComment(ctx, tx, taskservice.DisableArgs{
			Actor: w.actor, CommentID: row.ID, CommentPublicID: row.PublicID, TaskID: ref.id, TaskPublicID: ref.pub,
		}); err != nil {
			return err
		}
		w.audit(ctx, tx, mutationlog.Mutation{
			AuditAction: "comment.delete", ResourceType: "comment", ResourceID: row.PublicID.String(),
			Payload:  map[string]any{"taskId": ref.pub.String(), "commentId": row.PublicID.String()},
			CallSite: "asanaimport.Write (taskservice.DisableComment appended the event)",
		})
		return nil
	})
	if err != nil && !errors.Is(err, taskservice.ErrCommentNotFound) {
		w.fail(&c.Failed, "comment "+row.GID, err)
		return
	}
	c.Deleted++
}
