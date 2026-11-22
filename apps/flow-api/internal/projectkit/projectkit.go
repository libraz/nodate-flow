// Package projectkit writes project and project-member rows. The HTTP
// handlers and any other writer of these rows go through it so the rows are
// produced one way.
//
// The kit takes the caller's transaction and writes no event and no audit
// entry; the caller records its own audit entry after commit.
package projectkit

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

const (
	slugKey       = "uniq_projects_workspace_id_slug_active"
	identifierKey = "uniq_projects_workspace_id_identifier_active"
)

// ErrSlugTaken is returned when the workspace already has a live project
// with the slug.
var ErrSlugTaken = errors.New("projectkit: slug already taken")

// ErrIdentifierTaken is returned when the workspace already has a live
// project with the identifier.
var ErrIdentifierTaken = errors.New("projectkit: identifier already taken")

// CreateArgs carries the columns of a new project and the user enrolled as
// its lead.
type CreateArgs struct {
	WorkspaceID uint32
	Slug        string
	Identifier  sql.NullString
	Name        string
	Description sql.NullString
	Color       sql.NullString
	LeadUserID  uint32
}

// Project identifies a created project.
type Project struct {
	ID       uint32
	PublicID types.PublicID
}

// Create inserts the project and its lead membership on tx. A failure to
// enroll the lead fails the call, so the caller's rollback leaves no project
// behind.
func Create(ctx context.Context, tx *dbretry.Tx, args CreateArgs) (Project, error) {
	q := generated.New(tx)
	pub := types.New()
	id, err := q.CreateProject(ctx, generated.CreateProjectParams{
		PublicID:    pub,
		WorkspaceID: args.WorkspaceID,
		Slug:        args.Slug,
		Identifier:  args.Identifier,
		Name:        args.Name,
		Description: args.Description,
		Color:       args.Color,
	})
	if err != nil {
		if apierr.IsDuplicateEntry(err) {
			key, _ := apierr.DuplicateKey(err)
			switch key {
			case slugKey:
				return Project{}, ErrSlugTaken
			case identifierKey:
				return Project{}, ErrIdentifierTaken
			}
		}
		return Project{}, err
	}
	projectID := uint32(id) //#nosec G115 -- LastInsertId for projects.id (INT UNSIGNED), fits uint32
	if _, err := addMember(ctx, q, MemberArgs{
		WorkspaceID: args.WorkspaceID,
		ProjectID:   projectID,
		UserID:      args.LeadUserID,
		Role:        generated.ProjectMembersRoleLead,
		PublicID:    types.New(),
		AddedAt:     time.Now(),
	}); err != nil {
		return Project{}, err
	}
	return Project{ID: projectID, PublicID: pub}, nil
}

// MemberArgs identifies a membership to add. PublicID is the id reported to
// the client for a new row; AddedAt is when the grant is recorded.
type MemberArgs struct {
	WorkspaceID uint32
	ProjectID   uint32
	UserID      uint32
	Role        generated.ProjectMembersRole
	PublicID    types.PublicID
	AddedAt     time.Time
}

// AddMember adds the user to the project on tx. It reports added=false and
// writes nothing when the user is already a live member.
func AddMember(ctx context.Context, tx *dbretry.Tx, args MemberArgs) (bool, error) {
	return addMember(ctx, generated.New(tx), args)
}

func addMember(ctx context.Context, q *generated.Queries, args MemberArgs) (bool, error) {
	_, err := q.FindProjectMemberByUserId(ctx, generated.FindProjectMemberByUserIdParams{
		ProjectID: args.ProjectID,
		UserID:    args.UserID,
	})
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if _, err := q.AddProjectMember(ctx, generated.AddProjectMemberParams{
		PublicID:    args.PublicID,
		WorkspaceID: args.WorkspaceID,
		ProjectID:   args.ProjectID,
		UserID:      args.UserID,
		Role:        args.Role,
		AddedAt:     sql.NullTime{Time: args.AddedAt, Valid: true},
	}); err != nil {
		return false, err
	}
	return true, nil
}
