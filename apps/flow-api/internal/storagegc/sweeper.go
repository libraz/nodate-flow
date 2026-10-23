// Package storagegc reclaims object-storage rows and the blobs behind
// them.
//
// Three kinds of residue accumulate. The schema comment on
// storage_objects and the attachment delete paths rely on a sweeper
// hard-deleting a row once nothing references it, and a failed
// RemoveObject keeps the row so the blob is retried on the next pass.
// Separately, every presigned upload URL is handed out against a row
// committed before the bytes arrive, so a client that asks for a URL and never uploads leaves a reservation
// whose declared size nobody checked.
//
// The second kind is what makes the first urgent. A member can declare
// one byte, send ten gigabytes to the URL, skip the confirm call that
// would measure it, and repeat: without reclamation the only bound on
// what one account can store is how fast it can ask.
//
// The third is a blob whose rows a workspace or user teardown already
// deleted: the teardown queues its key in storage_blob_removals, and the
// sweeper retries it until the object store confirms the removal.
package storagegc

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// Defaults for the sweep cadence and its batch size.
const (
	// DefaultInterval is how often the sweeper runs. Reclamation is
	// housekeeping: running it often enough to bound growth matters,
	// running it promptly does not.
	DefaultInterval = 10 * time.Minute

	// DefaultReservationTTL is how long a row may sit unconfirmed
	// before it is treated as abandoned.
	//
	// The number is not a guess about client behaviour: it is the
	// lifetime of the presigned URL the row is currently serving, plus
	// room for a transfer already in flight when the URL expired. Once
	// that URL is dead no upload can arrive against the row, so waiting
	// longer only delays the reclaim. Setting it shorter is the
	// dangerous direction — it deletes rows out from under uploads that
	// are still legitimately running, and the user sees an attachment
	// vanish mid-transfer.
	//
	// "Currently serving", not "was created for": a presign that lands
	// on an unconfirmed row reuses it and mints a new URL against the
	// same key rather than allocating a second row. The age the sweep
	// measures is therefore taken from the row's last update, which is
	// when the newest URL was issued.
	DefaultReservationTTL = 45 * time.Minute

	// DefaultBatchSize caps how many rows one pass reclaims, so a
	// backlog is worked off over several passes instead of one long
	// transaction.
	DefaultBatchSize = 100
)

// ObjectRemover is the slice of the storage client the sweeper needs.
type ObjectRemover interface {
	RemoveObject(ctx context.Context, key string) error
}

// Sweeper reclaims unconfirmed reservations and unreferenced rows.
type Sweeper struct {
	DB      *sql.DB
	Queries *generated.Queries
	Storage ObjectRemover
	Logger  *slog.Logger

	// Interval, ReservationTTL and BatchSize fall back to their
	// Default* constants when left zero.
	Interval       time.Duration
	ReservationTTL time.Duration
	BatchSize      int32

	stopOnce sync.Once
	doneOnce sync.Once
	stopCh   chan struct{}
	done     chan struct{}
}

// New builds a sweeper with the default cadence.
func New(db *sql.DB, q *generated.Queries, store ObjectRemover, logger *slog.Logger) *Sweeper {
	return &Sweeper{
		DB:      db,
		Queries: q,
		Storage: store,
		Logger:  logger,
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start runs the sweep loop in its own supervised goroutine and
// returns.
//
// Supervised because the loop is the only thing bounding what one
// account can store. A pass that panics on one malformed row and is
// never restarted leaves the ceiling off for every tenant, and the
// only symptom is a bucket that keeps growing — the same silence the
// missing sweeper produced in the first place.
func (s *Sweeper) Start(ctx context.Context) {
	go bgloop.Run(ctx, "storage.sweeper", s.Logger, s.loop)
	s.Logger.Info("storage sweeper started",
		slog.Duration("interval", s.interval()),
		slog.Duration("reservation_ttl", s.reservationTTL()),
	)
}

// Stop signals the loop to exit and waits for the in-flight pass.
func (s *Sweeper) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
		s.Logger.Warn("storage sweeper: shutdown timeout exceeded; abandoning in-flight pass")
	}
}

func (s *Sweeper) loop(ctx context.Context) {
	// Once, not on every return: the supervisor restarts the loop after
	// a panic, and a second close would take the process down by the
	// route the supervisor exists to prevent.
	defer s.doneOnce.Do(func() { close(s.done) })
	ticker := time.NewTicker(s.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			if _, err := s.RunOnce(ctx); err != nil {
				s.Logger.Error("storage sweeper: pass failed", slog.String("err", err.Error()))
			}
		}
	}
}

// Result reports what one pass reclaimed.
type Result struct {
	Reservations int
	Unreferenced int
	// QueuedBlobs counts blobs removed from storage_blob_removals, the
	// keys a teardown left behind after deleting the rows that named them.
	QueuedBlobs int
}

// reclaimKind picks the predicate the row delete is made conditional
// on. The two kinds of residue are racing with different things, so one
// guard cannot serve both.
type reclaimKind int

const (
	// reservationKind is a row whose upload never arrived. Nobody new
	// can be pointing at it — an unconfirmed row is deliberately not a
	// dedup candidate — so the only way it stops being collectable is a
	// confirm landing while this pass runs. Guard on the predicate that
	// selected it and let the confirm win.
	reservationKind reclaimKind = iota

	// unreferencedKind is a row every referrer has already left. Here a
	// concurrent upload really can adopt it, and adoption shows up as
	// ref_count climbing back above zero.
	unreferencedKind
)

// RunOnce reclaims one batch of each kind and reports the counts.
//
// The two kinds are swept in the same pass because they are the same
// question asked twice — is anything still going to use this blob —
// and a second scanner walking the same table on its own schedule is
// how two answers to one question start to disagree.
func (s *Sweeper) RunOnce(ctx context.Context) (Result, error) {
	var out Result

	// The cutoff is always present; it arrives as a NullTime only
	// because it is compared against updated_at, which the schema
	// declares nullable even though the column defaults on insert and
	// restamps on update, so no row ever carries a NULL there.
	cutoff := sql.NullTime{Time: time.Now().UTC().Add(-s.reservationTTL()), Valid: true}
	stale, err := s.Queries.ListUnconfirmedStorageObjects(ctx, generated.ListUnconfirmedStorageObjectsParams{
		Cutoff: cutoff,
		Limit:  s.batchSize(),
	})
	if err != nil {
		return out, err
	}
	for _, row := range stale {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if s.reclaim(ctx, row.ID, row.StorageKey, reservationKind, "unconfirmed reservation") {
			out.Reservations++
		}
	}

	orphans, err := s.Queries.ListUnreferencedStorageObjects(ctx, s.batchSize())
	if err != nil {
		return out, err
	}
	for _, row := range orphans {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if s.reclaim(ctx, row.ID, row.StorageKey, unreferencedKind, "unreferenced object") {
			out.Unreferenced++
		}
	}

	queued, err := s.drainQueuedRemovals(ctx)
	out.QueuedBlobs = queued
	if err != nil {
		return out, err
	}

	if out.Reservations > 0 || out.Unreferenced > 0 || out.QueuedBlobs > 0 {
		s.Logger.Info("storage sweeper: reclaimed",
			slog.Int("reservations", out.Reservations),
			slog.Int("unreferenced", out.Unreferenced),
			slog.Int("queued_blobs", out.QueuedBlobs),
		)
	}
	return out, nil
}

// drainQueuedRemovals removes one batch of blobs a teardown queued in
// storage_blob_removals. Each record is dropped only after its blob is
// gone, so a failed removal is retried on the next pass; removing a key
// that is already absent is not an error. Without an object store
// nothing can be removed, so the records stay.
func (s *Sweeper) drainQueuedRemovals(ctx context.Context) (int, error) {
	if s.Storage == nil {
		return 0, nil
	}
	rows, err := s.Queries.ListStorageBlobRemovals(ctx, s.batchSize())
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if err := s.Storage.RemoveObject(ctx, row.StorageKey); err != nil {
			s.Logger.Warn("storage sweeper: queued blob remove failed; kept for retry",
				slog.String("storage_key", row.StorageKey),
				slog.String("err", err.Error()))
			continue
		}
		// affected-rows: not-applicable — a teardown clearing its own
		// records after a clean sweep can take this row first; the blob is
		// gone either way.
		if _, err := s.Queries.DeleteStorageBlobRemoval(ctx, row.ID); err != nil {
			s.Logger.Error("storage sweeper: drop queued removal", slog.String("err", err.Error()))
			continue
		}
		removed++
	}
	return removed, nil
}

// reclaim drops the rows that reference a storage object, then the
// object row, then the blob, and commits only once the blob is gone.
// It reports whether the row went.
//
// The order is forced by the schema: attachments reference
// storage_objects with ON DELETE RESTRICT, so the referrers go first.
// A row is never committed away while its blob may still exist: if the
// object store fails, everything rolls back and the next pass retries
// from the same row.
func (s *Sweeper) reclaim(ctx context.Context, id uint32, key string, kind reclaimKind, reason string) bool {
	log := s.Logger.With(
		slog.Uint64("storage_object_id", uint64(id)),
		slog.String("reason", reason),
	)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Error("storage sweeper: begin", slog.String("err", err.Error()))
		return false
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.Queries.WithTx(tx)

	// affected-rows: not-applicable — the collector drops whatever still
	// refers to the blob it is reclaiming, and a blob with no task
	// attachments is the ordinary case rather than a missing resource.
	if _, err := qtx.DeleteAttachmentsForStorageObject(ctx, id); err != nil {
		log.Error("storage sweeper: drop task attachments", slog.String("err", err.Error()))
		return false
	}
	// affected-rows: not-applicable — same reason on the calendar side; the
	// delete that decides whether the blob goes is the one below, and it
	// does read its count.
	if _, err := qtx.DeleteCalendarEventAttachmentsForStorageObject(ctx, id); err != nil {
		log.Error("storage sweeper: drop event attachments", slog.String("err", err.Error()))
		return false
	}

	var (
		affected int64
		delErr   error
	)
	switch kind {
	case reservationKind:
		affected, delErr = qtx.DeleteUnconfirmedStorageObjectByID(ctx, id)
	case unreferencedKind:
		affected, delErr = qtx.DeleteStorageObjectByID(ctx, id)
	}
	if delErr != nil {
		log.Error("storage sweeper: delete row", slog.String("err", delErr.Error()))
		return false
	}
	if affected == 0 {
		// The row stopped being collectable between the listing and
		// now: a confirm landed on the reservation, or a fresh upload of
		// the same content deduped onto the unreferenced row. Either way
		// the guard held and the rollback puts the referrers back.
		return false
	}
	// The blob goes while the row delete is still uncommitted. The row
	// lock keeps a dedup adoption or a confirm out until the commit, and
	// a failed object-store call rolls the delete back so the row, which
	// names the key, is picked up again by the next pass. A commit that
	// fails after the blob is gone leaves the same row, and removing an
	// absent key is not an error.
	if s.Storage != nil && key != "" {
		if err := s.Storage.RemoveObject(ctx, key); err != nil {
			log.Warn("storage sweeper: blob remove failed; row kept for retry",
				slog.String("storage_key", key),
				slog.String("err", err.Error()))
			return false
		}
	}
	if err := tx.Commit(); err != nil {
		log.Error("storage sweeper: commit", slog.String("err", err.Error()))
		return false
	}
	return true
}

func (s *Sweeper) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return DefaultInterval
}

func (s *Sweeper) reservationTTL() time.Duration {
	if s.ReservationTTL > 0 {
		return s.ReservationTTL
	}
	return DefaultReservationTTL
}

func (s *Sweeper) batchSize() int32 {
	if s.BatchSize > 0 {
		return s.BatchSize
	}
	return DefaultBatchSize
}
