package e2e

import (
	"context"
	"database/sql"
	"image/color"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storagegc"
)

// newSweeper builds a sweeper bound to the shared test database and
// MinIO, driven with RunOnce so a pass is one observable step.
func newSweeper(t *testing.T) *storagegc.Sweeper {
	t.Helper()
	return storagegc.New(testDB, generated.New(testDB), testStorage.Flow, slog.Default())
}

// storageObjectExists reports whether the row is still in the database.
func storageObjectExists(ctx context.Context, t *testing.T, key string) bool {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM storage_objects WHERE storage_key = ?`, key).Scan(&n))
	return n > 0
}

// TestSweeperReclaimsAnAbandonedReservation is the bound on what one
// account can store.
//
// The presigned PUT binds the body's hash but not its length, and the
// only size anyone checks at presign time is the one the client
// declared. Declare a byte, send whatever fits, skip the confirm call
// that would measure it, repeat — with nothing reclaiming the result
// the ceiling is not a ceiling. Reclamation is what makes the attack
// cost the attacker something: the bytes come back.
func TestSweeperReclaimsAnAbandonedReservation(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()
	lockStorageSweepPass(t)

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Reclaimed")
	payload := makePNG(t, 8, 8, color.RGBA{R: 41, G: 42, B: 43, A: 255})

	res := presignAttachment(t, tt.AccessToken, taskID, "hoard.png", "image/png", payload)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)
	testStorage.MustExist(t, res.StorageKey)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Age the reservation past the presigned URL's lifetime: no upload
	// can arrive against it any more, so nothing is being interrupted.
	//
	// updated_at is the column the sweep ages rows by, because a row
	// gets handed out again every time a presign lands on it. Writing it
	// explicitly is also what suppresses ON UPDATE CURRENT_TIMESTAMP,
	// which would otherwise stamp this very statement as the moment the
	// row was last touched and leave it younger than the cutoff.
	_, err := testDB.ExecContext(ctx,
		`UPDATE storage_objects SET created_at = ?, updated_at = ? WHERE storage_key = ?`,
		time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-2*time.Hour), res.StorageKey)
	require.NoError(t, err)

	// A pass sweeps the whole instance, so the TTL is what keeps this
	// test off other tests' rows: an hour is far longer than a suite
	// runs, so the only row on the wrong side of the cutoff is the one
	// back-dated above. The counts in Result are for the same reason not
	// worth asserting on — they include whatever the rest of the suite
	// happened to leave behind. What this test is about is the row.
	sweeper := newSweeper(t)
	sweeper.ReservationTTL = time.Hour
	_, err = sweeper.RunOnce(ctx)
	require.NoError(t, err)

	require.False(t, storageObjectExists(ctx, t, res.StorageKey),
		"an upload nobody ever confirmed must not hold a row forever")
	testStorage.MustNotExist(t, res.StorageKey)
}

// TestSweeperLeavesAConfirmedUploadAlone is the assertion that keeps the
// one above from being a licence to delete. A measured upload is real
// content someone is using, and no amount of age makes it collectable.
func TestSweeperLeavesAConfirmedUploadAlone(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()
	lockStorageSweepPass(t)

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Kept")
	payload := makePNG(t, 8, 8, color.RGBA{R: 51, G: 52, B: 53, A: 255})

	res := presignAttachment(t, tt.AccessToken, taskID, "keep.png", "image/png", payload)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)
	confirmAttachment(t, tt.AccessToken, taskID, res.AttachmentID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Both age columns, so the row is old by whichever one the sweep
	// measures and the claim below is about the confirmation rather than
	// about having picked a column the sweep ignores.
	_, err := testDB.ExecContext(ctx,
		`UPDATE storage_objects SET created_at = ?, updated_at = ? WHERE storage_key = ?`,
		time.Now().UTC().Add(-30*24*time.Hour), time.Now().UTC().Add(-30*24*time.Hour), res.StorageKey)
	require.NoError(t, err)

	sweeper := newSweeper(t)
	sweeper.ReservationTTL = time.Hour
	_, err = sweeper.RunOnce(ctx)
	require.NoError(t, err)

	require.True(t, storageObjectExists(ctx, t, res.StorageKey),
		"a confirmed upload is content in use, however old the row is")
	testStorage.MustExist(t, res.StorageKey)
}

// TestSweeperSparesAReservationJustHandedOutAgain is the other edge of
// the reclaim, and the one that loses data when it is wrong.
//
// A reservation is not used once. When a presign finds a row whose
// upload never arrived it does not allocate a second row — it reuses
// that one and mints a fresh URL against the same key, which is how a
// reservation somebody abandoned gets repaired instead of poisoning the
// content for the whole workspace. So a row can be hours old and still
// be serving a URL issued seconds ago.
//
// Age the row from its creation and that second upload is born already
// past the cutoff: the sweep deletes the row, drops every attachment
// pointing at it and removes the blob, and the person who just uploaded
// watches their file disappear with nothing logged against them. The
// reclaim has to be measured from when the row was last handed out.
func TestSweeperSparesAReservationJustHandedOutAgain(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()
	lockStorageSweepPass(t)

	tt := newTenant(t)
	firstTask := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Abandoned")
	secondTask := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Repaired")
	payload := makePNG(t, 8, 8, color.RGBA{R: 81, G: 82, B: 83, A: 255})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Someone asks for a URL and walks away.
	abandoned := presignAttachment(t, tt.AccessToken, firstTask, "ghost.png", "image/png", payload)
	require.False(t, abandoned.Deduplicated)
	testStorage.MustNotExist(t, abandoned.StorageKey)

	// Long enough ago that the row is a reclaim candidate.
	_, err := testDB.ExecContext(ctx,
		`UPDATE storage_objects SET created_at = ?, updated_at = ? WHERE storage_key = ?`,
		time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-2*time.Hour), abandoned.StorageKey)
	require.NoError(t, err)

	// Now somebody uploads the same content. The presign repairs the
	// abandoned row in place rather than allocating a new one, so this
	// upload is running against a row created two hours ago.
	repaired := presignAttachment(t, tt.AccessToken, secondTask, "real.png", "image/png", payload)
	require.False(t, repaired.Deduplicated,
		"a row whose object was never uploaded must hand back a way to fill it")
	require.NotEmpty(t, repaired.UploadURL)
	require.Equal(t, abandoned.StorageKey, repaired.StorageKey,
		"the repair must reuse the key the existing row already points at")
	uploadViaPresignedURL(t, repaired.UploadURL, "image/png", payload, repaired.RequiredHeaders)

	sweeper := newSweeper(t)
	sweeper.ReservationTTL = time.Hour
	_, err = sweeper.RunOnce(ctx)
	require.NoError(t, err)

	require.True(t, storageObjectExists(ctx, t, repaired.StorageKey),
		"a row serving a URL issued seconds ago is not abandoned, whatever its creation time says")
	testStorage.MustExist(t, repaired.StorageKey)

	// The row surviving is only half of it: reclaim drops every
	// attachment pointing at the row before the row itself, so this is
	// what the second uploader would actually have lost.
	var listed struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+secondTask+"/attachments",
		tt.AccessToken, nil, &listed)
	found := false
	for _, a := range listed.Attachments {
		if a.ID == repaired.AttachmentID {
			found = true
		}
	}
	require.True(t, found,
		"an upload in progress must not have its attachment swept out from under it")

	// And the upload completes normally, which is the whole point of
	// having left it alone.
	confirmAttachment(t, tt.AccessToken, secondTask, repaired.AttachmentID)
}

// TestSweeperReclaimsAnUnreferencedRow covers the residue the delete
// paths leave when they get part way.
//
// Six places in the codebase say a sweeper hard-deletes a row once
// nothing references it, and until now none existed: an object-store
// call that failed, or a workspace torn down while the bucket was
// unreachable, left a blob no query could ever name again.
func TestSweeperReclaimsAnUnreferencedRow(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()
	lockStorageSweepPass(t)

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Orphan")
	payload := makePNG(t, 8, 8, color.RGBA{R: 61, G: 62, B: 63, A: 255})

	res := presignAttachment(t, tt.AccessToken, taskID, "orphan.png", "image/png", payload)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)
	confirmAttachment(t, tt.AccessToken, taskID, res.AttachmentID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The state an interrupted delete leaves: the referrer is gone and
	// the count is zero, but the row and its blob are still here.
	_, err := testDB.ExecContext(ctx,
		`DELETE a FROM attachments a
		   JOIN storage_objects s ON s.id = a.storage_object_id
		  WHERE s.storage_key = ?`, res.StorageKey)
	require.NoError(t, err)
	_, err = testDB.ExecContext(ctx,
		`UPDATE storage_objects SET ref_count = 0 WHERE storage_key = ?`, res.StorageKey)
	require.NoError(t, err)

	// No count assertion here either: the unreferenced sweep has no age
	// cutoff to fence it in, so a pass takes every orphan the rest of the
	// suite has left lying around along with this one. That the row is
	// gone afterwards is the claim; how many went with it is not.
	_, err = newSweeper(t).RunOnce(ctx)
	require.NoError(t, err)

	require.False(t, storageObjectExists(ctx, t, res.StorageKey),
		"a row nothing references must not keep its blob alive")
	testStorage.MustNotExist(t, res.StorageKey)
}

// TestSweeperKeepsAReferencedRow is the complement: a row someone is
// still pointing at survives, so the sweep cannot be mistaken for a
// delete-everything pass.
func TestSweeperKeepsAReferencedRow(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()
	lockStorageSweepPass(t)

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Referenced")
	payload := makePNG(t, 8, 8, color.RGBA{R: 71, G: 72, B: 73, A: 255})

	res := presignAttachment(t, tt.AccessToken, taskID, "live.png", "image/png", payload)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)
	confirmAttachment(t, tt.AccessToken, taskID, res.AttachmentID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := newSweeper(t).RunOnce(ctx)
	require.NoError(t, err)

	require.True(t, storageObjectExists(ctx, t, res.StorageKey))
	testStorage.MustExist(t, res.StorageKey)

	var refCount sql.NullInt64
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT ref_count FROM storage_objects WHERE storage_key = ?`, res.StorageKey).Scan(&refCount))
	require.EqualValues(t, 1, refCount.Int64)

	// And the attachment is still listed, so the sweep did not quietly
	// take the file away from the task it belongs to.
	var listed struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/attachments",
		tt.AccessToken, nil, &listed)
	found := false
	for _, a := range listed.Attachments {
		if a.ID == res.AttachmentID {
			found = true
		}
	}
	require.True(t, found, "a live attachment must survive a sweep")
}
