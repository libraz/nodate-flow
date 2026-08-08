package e2e

import (
	"sync"
	"testing"
)

// Three of the background passes this suite drives are instance-wide:
// the auto-action executor, the storage sweeper and the calendar
// reminder tick each walk every workspace in the database, which is
// what they do in production. Owning a tenant is therefore not enough
// isolation — a pass raised by one test walks the rows of every other
// test running beside it.
//
// That alone would be harmless if a pass decided and acted on a row at
// the same instant. It does not: each pass reads its whole work set
// first and processes it afterwards, so between the read and the act
// there is a window in which the row can change and the pass will still
// act on what it read. The failures that come out of it look like the
// system under test misbehaving:
//
//   - The executor resolves every workspace's confidence threshold
//     during enumeration. A workspace enumerated before its test has
//     written ai_settings resolves to the enumerating pass's global
//     threshold, and keeps it — so the sibling pass applies an action
//     the workspace's own configured threshold forbids, and the test
//     that configured it sees its task moved.
//   - The sweeper lists reclaim candidates by age up front. A row
//     back-dated past the cutoff and then repaired by a fresh presign
//     is already on a sibling pass's list, and gets deleted along with
//     the attachments pointing at it.
//   - The reminder tick claims an occurrence and fans it out to that
//     event's own recipients. A sibling tick that reaches another
//     test's event first takes the claim, and the test's own tick finds
//     nothing left to fire.
//
// So the critical section has to cover the seeding too, not just the
// call: closing it around the pass alone leaves the window wide open.
// Each test holds its lock from before it writes any state the pass can
// see, through to its last assertion.
//
// One mutex per pass rather than one for all three: they read disjoint
// tables and cannot disturb each other, so a shared lock would only
// cost parallelism. t.Parallel() stays on every one of these tests —
// they are serialized against their own kind by the lock and still run
// alongside the rest of the suite.
var (
	autoActionPassMu       sync.Mutex
	storageSweepPassMu     sync.Mutex
	calendarReminderPassMu sync.Mutex
)

// lockAutoActionPass serializes tests that drive
// autoactions.Executor.RunOnce. Call it after t.Parallel() (a lock held
// across the parallel park would block every sibling for nothing) and
// before seeding anything the executor evaluates: workspace ai_settings,
// auto_action_rules, or a back-dated task.
func lockAutoActionPass(t *testing.T) {
	t.Helper()
	autoActionPassMu.Lock()
	t.Cleanup(autoActionPassMu.Unlock)
}

// lockStorageSweepPass serializes tests that drive
// storagegc.Sweeper.RunOnce. Held from before the first presign, since
// the reservation row a sibling pass could reclaim exists from that
// moment on.
func lockStorageSweepPass(t *testing.T) {
	t.Helper()
	storageSweepPassMu.Lock()
	t.Cleanup(storageSweepPassMu.Unlock)
}

// lockCalendarReminderPass serializes tests that drive
// notifications.CheckAndNotify. Held from before the calendar event is
// inserted: an event whose reminder window is already open is claimable
// by any tick from the instant it exists.
func lockCalendarReminderPass(t *testing.T) {
	t.Helper()
	calendarReminderPassMu.Lock()
	t.Cleanup(calendarReminderPassMu.Unlock)
}
