package asanaimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/labelkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

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
