// Package taskdesc is the single writer for rows in
// task_description_versions.
//
// A version row is a body the task has held. The newest version_number is
// the body the task is carrying now, which is the reading the restore path
// already implements: restoring an older version writes that body onto the
// task and appends it again as the newest version, rather than rewinding
// the history to it.
//
// The snapshot has to be written inside the transaction that writes the
// description, so [Snapshot] takes the queries handle and returns its
// error rather than absorbing it. A description that committed without its
// snapshot is a body no restore can ever return to, and nothing later can
// reconstruct it.
//
// [Announce] is the other half: the row is written from several transports
// and the event naming it is one shape, so it is assembled here rather than
// at each of them.
package taskdesc

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// Version identifies the row [Snapshot] appended, for callers that name it
// in a response or an event payload. The zero value means no row was
// written, which is what an empty body produces.
type Version struct {
	PublicID types.PublicID
	Number   uint32
}

// Actor identifies the author of a description version. Agent IDs are
// accompanied by a captured public byline so a later agent deletion does
// not erase who wrote an older version. Exactly one user/agent ID may be
// set; both empty is reserved for system writes.
type Actor struct {
	UserID        sql.NullInt32
	AgentID       sql.NullInt32
	AgentPublicID types.PublicID
	AgentName     sql.NullString
}

func (a Actor) validate() error {
	if a.UserID.Valid && a.AgentID.Valid {
		return fmt.Errorf("taskdesc: author cannot be both a user and an agent")
	}
	if a.UserID.Valid && a.UserID.Int32 <= 0 {
		return fmt.Errorf("taskdesc: user author id must be positive")
	}
	if a.AgentID.Valid {
		if a.AgentID.Int32 <= 0 || a.AgentPublicID == (types.PublicID{}) || !a.AgentName.Valid || a.AgentName.String == "" {
			return fmt.Errorf("taskdesc: agent author requires a valid public byline")
		}
	} else if a.AgentPublicID != (types.PublicID{}) || a.AgentName.Valid {
		return fmt.Errorf("taskdesc: agent byline supplied without an agent author")
	}
	return nil
}

// Snapshot appends body as the task's newest description version.
//
// An empty body is not a version and is skipped: the column records a
// description the task held, and "no description" is the absence of one.
// Callers may pass an empty body unconditionally rather than repeating
// that test at each site, and read the zero [Version] as "nothing to name".
//
// author may be invalid for a body a process wrote rather than a person;
// the column is nullable and the foreign key clears it when the user is
// removed.
//
// q must be bound to the transaction that writes tasks.description. The
// version number comes from MAX(version_number) + 1 read through the same
// handle, so two concurrent writes to one task serialise on the unique key
// over (task_id, version_number) instead of both claiming the same number.
func Snapshot(ctx context.Context, q *generated.Queries, workspaceID, taskID uint32, author sql.NullInt32, body string) (Version, error) {
	return SnapshotWithActor(ctx, q, workspaceID, taskID, Actor{UserID: author}, body)
}

// SnapshotWithActor appends body as the task's newest description
// version and attributes it to a human, an agent, or the system.
// Agent public identity and name are copied onto the version so the
// history retains its byline when the agent row is later deleted.
func SnapshotWithActor(ctx context.Context, q *generated.Queries, workspaceID, taskID uint32, actor Actor, body string) (Version, error) {
	if err := actor.validate(); err != nil {
		return Version{}, err
	}
	if body == "" {
		return Version{}, nil
	}
	next, err := q.NextDescriptionVersionNumber(ctx, taskID)
	if err != nil {
		return Version{}, fmt.Errorf("taskdesc: next version number: %w", err)
	}
	number := uint32(next) //#nosec G115 -- per-task version sequence, fits uint32
	pub := types.New()
	if _, err := q.CreateDescriptionVersion(ctx, generated.CreateDescriptionVersionParams{
		PublicID:            pub,
		WorkspaceID:         workspaceID,
		TaskID:              taskID,
		AuthorUserID:        actor.UserID,
		AuthorAgentID:       actor.AgentID,
		AuthorAgentPublicID: actor.AgentPublicID,
		AuthorAgentName:     actor.AgentName,
		VersionNumber:       number,
		BodyLength:          uint32(len(body)), //#nosec G115 -- description body capped by request validation, fits uint32
		Body:                body,
	}); err != nil {
		return Version{}, fmt.Errorf("taskdesc: create version: %w", err)
	}
	return Version{PublicID: pub, Number: number}, nil
}

// Announcement is the task a version was written for, named the way the
// event log needs it: the internal id for the events row's own column, the
// public id for the payload, which carries public ids only.
type Announcement struct {
	WorkspaceID  uint32
	TaskID       uint32
	TaskPublicID types.PublicID
	// Author is the user whose write produced the version, or nil for a
	// body a process wrote. It mirrors the author passed to [Snapshot].
	Author  *int64
	Version Version
}

// Announce appends the event naming a version [Snapshot] wrote.
//
// The zero [Version] is what an empty body produces, and a version no row
// exists for is not one to announce, so this is a no-op there — callers can
// place it beside the snapshot without repeating that test.
//
// tx is the boundary the version row commits on, and the event goes inside
// it: the row and the timeline entry naming it are one fact, and an append
// made after the commit could drop the entry for a body that is already
// stored, leaving nothing to reconstruct it from. Nothing is derived from
// this kind, so the reverse case — an event for a version the transaction
// went on to roll back — is the one worth ruling out.
//
// The restore path writes a version too and does not call this: it appends
// description.version.restored, whose payload already names the version it
// created, and a second entry would put one write on the timeline twice.
func Announce(ctx context.Context, tx dbretry.CommitBoundary, a Announcement) error {
	actor, err := actorFromLegacyUser(a.Author)
	if err != nil {
		return err
	}
	return AnnounceWithActor(ctx, tx, a, actor)
}

// AnnounceWithActor appends the timeline event naming a description
// version, using the same human/agent/system identity as SnapshotWithActor.
func AnnounceWithActor(ctx context.Context, tx dbretry.CommitBoundary, a Announcement, actor Actor) error {
	if err := actor.validate(); err != nil {
		return err
	}
	if a.Author != nil && (!actor.UserID.Valid || int64(actor.UserID.Int32) != *a.Author) {
		return fmt.Errorf("taskdesc: announcement author differs from event actor")
	}
	if a.Version.Number == 0 {
		return nil
	}
	taskID := int64(a.TaskID)
	var userID, agentID *int64
	if actor.UserID.Valid {
		id := int64(actor.UserID.Int32)
		userID = &id
	}
	if actor.AgentID.Valid {
		id := int64(actor.AgentID.Int32)
		agentID = &id
	}
	return eventbus.Append(ctx, tx, eventbus.Event{
		Type:         eventbus.DescriptionVersionCreated,
		WorkspaceID:  a.WorkspaceID,
		ActorUserID:  userID,
		ActorAgentID: agentID,
		TaskID:       &taskID,
		Payload: map[string]any{
			"taskId":        a.TaskPublicID.String(),
			"versionId":     a.Version.PublicID.String(),
			"versionNumber": a.Version.Number,
		},
	})
}

func actorFromLegacyUser(author *int64) (Actor, error) {
	if author == nil {
		return Actor{}, nil
	}
	if *author <= 0 || *author > int64(^uint32(0)>>1) {
		return Actor{}, fmt.Errorf("taskdesc: invalid legacy user author id")
	}
	return Actor{UserID: sql.NullInt32{Int32: int32(*author), Valid: true}}, nil //#nosec G115 -- range checked above
}
