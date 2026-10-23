package labels

import (
	"context"
	"database/sql"
	"errors"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// maxParentWalk bounds the ancestor walk so a cycle already stored in the
// table cannot spin the request.
const maxParentWalk = 64

// CheckAttachScope reports why a label cannot join a task: a label scoped
// to a project attaches only to tasks of that project. It returns nil when
// the attach is allowed.
func CheckAttachScope(labelProject sql.NullInt32, taskProject uint32) *apierrors.Spec {
	if labelProject.Valid && uint32(labelProject.Int32) != taskProject { //#nosec G115 -- project_id is projects.id (INT UNSIGNED)
		return apierrors.WsLabelScopeMismatch
	}
	return nil
}

// CheckParent reports why parentID cannot become the parent of the label
// selfID (0 on create): the parent must sit in the label's own scope and
// must not be the label itself or one of its descendants. It returns nil
// when the parent is acceptable; a non-nil error is an unexpected failure.
func CheckParent(ctx context.Context, db *sql.DB, wsID, selfID uint32, labelProject, parentProject sql.NullInt32, parentID uint32) (*apierrors.Spec, error) {
	if labelProject != parentProject {
		return apierrors.WsLabelScopeMismatch, nil
	}
	const q = `SELECT parent_label_id FROM labels WHERE workspace_id = ? AND id = ? LIMIT 1`
	current := parentID
	for range maxParentWalk {
		if selfID != 0 && current == selfID {
			return apierrors.WsLabelCircularParent, nil
		}
		var next sql.NullInt32
		if err := db.QueryRowContext(ctx, q, wsID, current).Scan(&next); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}
		if !next.Valid {
			return nil, nil
		}
		current = uint32(next.Int32) //#nosec G115 -- parent_label_id is labels.id (INT UNSIGNED)
	}
	return apierrors.WsLabelCircularParent, nil
}
