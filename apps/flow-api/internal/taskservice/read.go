package taskservice

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
)

// Task is the stable service projection returned by Read and task writes.
// It contains only public identifiers and user-visible task fields.
type Task struct {
	ID           types.PublicID
	ProjectID    types.PublicID
	ProjectName  string
	Title        string
	Description  string
	Priority     int32
	Visibility   string
	DerivedState string
	DueOn        string
	StartedOn    string
	CreatedAt    time.Time
	UpdatedAt    *time.Time
}

// Read authorizes and reads a task under the same transaction-current
// authority rows used by commands.
func (s *Service) Read(ctx context.Context, principal execution.Principal, target types.PublicID) (Task, error) {
	if err := s.checkReadiness(false); err != nil {
		return Task{}, err
	}
	var out Task
	err := dbretry.InTx(ctx, s.DB, "taskservice.Read", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := execution.AuthorizeTaskTx(ctx, tx, principal, target, execution.CapabilityReadTask); err != nil {
			return err
		}
		row, err := s.Queries.WithTx(tx.RawTx()).FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: principal.WorkspaceID(),
			PublicID:    target,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return apierrors.New(apierrors.WsTaskNotFound)
			}
			return err
		}
		out, err = taskFromRow(row)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return out, nil
}

func taskFromRow(row generated.FindTaskByPublicIdRow) (Task, error) {
	var projectID types.PublicID
	if err := projectID.Scan(row.ProjectPublicID); err != nil {
		return Task{}, err
	}
	var updatedAt *time.Time
	if row.UpdatedAt.Valid {
		v := row.UpdatedAt.Time
		updatedAt = &v
	}
	return Task{
		ID:           row.PublicID,
		ProjectID:    projectID,
		ProjectName:  row.ProjectName,
		Title:        row.Title,
		Description:  row.Description.String,
		Priority:     row.Priority,
		Visibility:   string(row.Visibility),
		DerivedState: string(row.DerivedState),
		DueOn:        dateOnly(row.DueOn),
		StartedOn:    dateOnly(row.StartedOn),
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    updatedAt,
	}, nil
}

func dateOnly(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return value.Time.Format("2006-01-02")
}
