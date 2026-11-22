// Package attachkit owns the writes behind a task attachment: reserving
// the content-addressed storage object and the attachment row that points
// at it, and confirming the object once its bytes are in the store.
//
// Reserve runs its own transactions because a lost storage_objects insert
// race is retried in a fresh one (see Reserve). It appends the timeline
// event inside the committing transaction; the audit row is the caller's,
// written with RecordTxAudit after Reserve returns. Uploading the bytes,
// signing URLs and translating errors stay with the caller.
//
// ReserveObject and StillStored are the storage-object half on its own,
// shared with the calendar attachment surface, whose attachment rows live
// in a different table.
package attachkit

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// MaxAttempts bounds the race-lost retry loop. Under REPEATABLE READ a
// same-transaction re-read cannot see the winner, so each retry is a fresh
// transaction; a two-way race converges on the second, three is a ceiling.
const MaxAttempts = 3

// ErrTaskGone is returned when the task was disabled or deleted after the
// caller resolved it.
var ErrTaskGone = errors.New("attachkit: task gone")

// ErrRaceLost is returned by ReserveObject when another transaction
// committed the same (workspace, sha256) object first. The caller must
// retry in a fresh transaction.
var ErrRaceLost = errors.New("attachkit: storage object race lost")

// ErrRetriesExhausted is returned by Reserve when every attempt lost the
// insert race.
var ErrRetriesExhausted = errors.New("attachkit: storage object race retries exhausted")

// ErrObjectRead wraps a failure reading the storage object row.
var ErrObjectRead = errors.New("attachkit: storage object read failed")

// ErrObjectWrite wraps a failure writing the storage object row.
var ErrObjectWrite = errors.New("attachkit: storage object write failed")

// ObjectStatter answers whether an object exists in the store. It is the
// subset of *storage.Client the dedup check needs.
type ObjectStatter interface {
	StatObject(ctx context.Context, key string) (int64, error)
}

// Object describes the content being reserved.
type Object struct {
	WorkspaceID uint32
	// WorkspacePublicID derives the content-addressed storage key.
	WorkspacePublicID types.PublicID
	// SHA256 is the 32-byte digest of the file body.
	SHA256 []byte
	// Size is the declared byte size, replaced by the measured one at
	// MarkUploaded.
	Size        uint64
	ContentType string
}

// Reservation is the storage object a reservation landed on.
type Reservation struct {
	StorageObjectID uint32
	StorageKey      string
	// Deduplicated is true when an existing row's ref_count was bumped
	// rather than a new row inserted.
	Deduplicated bool
	// UploadedAt is the existing row's confirmation stamp on a dedup hit.
	UploadedAt sql.NullTime
}

// ReserveObject takes one reference on the storage object for o inside
// q's transaction: it bumps the ref_count of an existing (workspace,
// sha256) row, or inserts a new row at ref_count 1 under the
// content-addressed key. It returns ErrRaceLost when the insert collides
// with a concurrently committed row, and wraps other failures in
// ErrObjectRead or ErrObjectWrite.
func ReserveObject(ctx context.Context, q *generated.Queries, o Object) (Reservation, error) {
	wsID := sql.NullInt32{Int32: int32(o.WorkspaceID), Valid: true} //#nosec G115 -- internal id, bounded by realistic deployments
	existing, findErr := q.FindStorageObjectByWorkspaceSha(ctx, generated.FindStorageObjectByWorkspaceShaParams{
		WorkspaceID: wsID,
		Sha256:      o.SHA256,
	})
	switch {
	case findErr == nil:
		if err := q.IncrementStorageObjectRefCount(ctx, existing.ID); err != nil {
			return Reservation{}, fmt.Errorf("%w: %w", ErrObjectWrite, err)
		}
		return Reservation{
			StorageObjectID: existing.ID,
			StorageKey:      existing.StorageKey,
			Deduplicated:    true,
			UploadedAt:      existing.UploadedAt,
		}, nil
	case errors.Is(findErr, sql.ErrNoRows):
	default:
		return Reservation{}, fmt.Errorf("%w: %w", ErrObjectRead, findErr)
	}

	key := storage.KeyForWorkspace(hex.EncodeToString(o.WorkspacePublicID[:]), hex.EncodeToString(o.SHA256))
	insRes, insErr := q.InsertStorageObject(ctx, generated.InsertStorageObjectParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		OwnerUserID: sql.NullInt32{},
		Sha256:      o.SHA256,
		ByteSize:    o.Size,
		ContentType: o.ContentType,
		StorageKey:  key,
	})
	switch {
	case insErr == nil:
	case apierr.IsDuplicateEntry(insErr):
		return Reservation{}, ErrRaceLost
	default:
		return Reservation{}, fmt.Errorf("%w: %w", ErrObjectWrite, insErr)
	}
	lastID, err := insRes.LastInsertId()
	if err != nil || lastID <= 0 {
		return Reservation{}, fmt.Errorf("%w: no insert id", ErrObjectWrite)
	}
	return Reservation{
		StorageObjectID: uint32(lastID), //#nosec G115 -- AUTO_INCREMENT id fits uint32 within realistic deployments
		StorageKey:      key,
	}, nil
}

// StillStored reports whether a dedup hit's promise holds: the row was
// confirmed and the store still has its object. An unconfirmed row is a
// reservation carrying only a declared size, and a confirmed one can lose
// its object to a lifecycle rule or partial restore. Either way the caller
// uploads again to the same key. Call it outside any transaction: it is a
// network round trip.
func StillStored(ctx context.Context, store ObjectStatter, r Reservation) bool {
	if !r.Deduplicated || !r.UploadedAt.Valid {
		return false
	}
	_, err := store.StatObject(ctx, r.StorageKey)
	return err == nil
}

// Args describes one task attachment.
type Args struct {
	WorkspaceID       uint32
	WorkspacePublicID types.PublicID
	TaskID            uint32
	TaskPublicID      types.PublicID
	Filename          string
	ContentType       string
	// Size is the declared byte size.
	Size uint64
	// SHA256 is the 32-byte digest of the file body.
	SHA256 []byte
	// SourceUID is the namespaced import origin; empty stores NULL.
	SourceUID string
	// UploaderUserID is stored as attachments.uploader_id.
	UploaderUserID uint32
	// Actor is who the timeline event is attributed to.
	Actor mutationlog.Actor
	// Store settles a dedup hit after commit.
	Store ObjectStatter
}

// Result identifies the reserved attachment.
type Result struct {
	AttachmentID       uint32
	AttachmentPublicID types.PublicID
	StorageObjectID    uint32
	StorageKey         string
	// Deduplicated is true when the attachment took a reference on an
	// existing storage object.
	Deduplicated bool
	// AlreadyUploaded is true when that object is confirmed and present
	// in the store, so the caller must not upload.
	AlreadyUploaded bool
}

// Reserve inserts the attachment row and its storage object reference and
// appends task.attachment.added, then settles a dedup hit against the
// store. The task row is locked first: the attachment insert takes a
// shared FK lock on it and the work-revision trigger then needs it
// exclusively, so two reservations on one task would otherwise deadlock.
func Reserve(ctx context.Context, db dbretry.TxBeginner, args Args) (Result, error) {
	attachPub := types.New()
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		var (
			resv     Reservation
			attachID int64
		)
		err := dbretry.InTx(ctx, db, "attachkit.Reserve", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			q := generated.New(tx)
			if _, err := q.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
				ID:          args.TaskID,
				WorkspaceID: args.WorkspaceID,
			}); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrTaskGone
				}
				return err
			}
			var err error
			resv, err = ReserveObject(ctx, q, Object{
				WorkspaceID:       args.WorkspaceID,
				WorkspacePublicID: args.WorkspacePublicID,
				SHA256:            args.SHA256,
				Size:              args.Size,
				ContentType:       args.ContentType,
			})
			if err != nil {
				return err
			}
			attachID, err = q.AddAttachment(ctx, generated.AddAttachmentParams{
				PublicID:        attachPub,
				WorkspaceID:     args.WorkspaceID,
				TaskID:          sql.NullInt32{Int32: int32(args.TaskID), Valid: true}, //#nosec G115 -- internal id, bounded by realistic deployments
				UploaderID:      args.UploaderUserID,
				StorageObjectID: resv.StorageObjectID,
				Filename:        args.Filename,
				SourceUid:       sql.NullString{String: args.SourceUID, Valid: args.SourceUID != ""},
			})
			if err != nil {
				return err
			}
			// Whether the caller still has to upload is settled after
			// commit, so the event carries only what this transaction
			// makes true.
			taskID := int64(args.TaskID)
			user, agent := actorFields(args.Actor)
			return eventbus.Append(ctx, tx, eventbus.Event{
				Type:         eventbus.TaskAttachmentAdded,
				WorkspaceID:  args.WorkspaceID,
				ActorUserID:  user,
				ActorAgentID: agent,
				TaskID:       &taskID,
				Payload: map[string]any{
					"taskId":       args.TaskPublicID.String(),
					"attachmentId": attachPub.String(),
					"filename":     args.Filename,
				},
			})
		})
		switch {
		case err == nil:
			return Result{
				AttachmentID:       uint32(attachID), //#nosec G115 -- AUTO_INCREMENT id fits uint32 within realistic deployments
				AttachmentPublicID: attachPub,
				StorageObjectID:    resv.StorageObjectID,
				StorageKey:         resv.StorageKey,
				Deduplicated:       resv.Deduplicated,
				AlreadyUploaded:    StillStored(ctx, args.Store, resv),
			}, nil
		case errors.Is(err, ErrRaceLost):
			continue
		default:
			return Result{}, err
		}
	}
	return Result{}, ErrRetriesExhausted
}

// MarkArgs names the storage object whose bytes were measured.
type MarkArgs struct {
	WorkspaceID uint32
	// TaskID, when non-zero, is locked before the stamp, in the order
	// Reserve takes them: the stamp bumps the task's work revision
	// through a trigger.
	TaskID          uint32
	StorageObjectID uint32
	// Size is the measured stored size; it replaces the declared one.
	Size uint64
}

// MarkUploaded stamps the storage object as present with its measured
// size, turning the reservation into a dedup candidate the sweeper leaves
// alone. It appends no event: the attachment reached the timeline when it
// was reserved. It returns ErrTaskGone when TaskID no longer resolves.
// The stamp's upload trigger also locks task rows for a shared object,
// so a deadlock with a concurrent confirm is retried.
func MarkUploaded(ctx context.Context, db dbretry.TxBeginner, args MarkArgs) error {
	return dbretry.InTx(ctx, db, "attachkit.MarkUploaded", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		q := generated.New(tx)
		if args.TaskID != 0 {
			if _, err := q.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
				ID:          args.TaskID,
				WorkspaceID: args.WorkspaceID,
			}); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrTaskGone
				}
				return err
			}
		}
		return q.MarkStorageObjectUploaded(ctx, generated.MarkStorageObjectUploadedParams{
			ByteSize: args.Size,
			ID:       args.StorageObjectID,
		})
	})
}

func actorFields(a mutationlog.Actor) (user, agent *int64) {
	if a.AgentID != 0 {
		v := int64(a.AgentID)
		return nil, &v
	}
	if a.UserID != 0 {
		v := int64(a.UserID)
		return &v, nil
	}
	return nil, nil
}
