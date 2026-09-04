// Attachment-presign security suite. These tests sit alongside
// attachment_dedup_test.go and probe the failure / abuse modes of the
// task-attachment presign endpoint:
//
//   - SigV4 signed-header binding (x-amz-content-sha256) keeps the
//     dedup row tied to the bytes the client claimed.
//   - Cross-workspace lookups never leak attachment metadata or
//     download URLs.
//   - Validation rejects malformed sha256, oversized files, blocked
//     extensions / MIME types, and absurd filenames.
//   - The (workspace_id, sha256) UNIQUE key + handler retry path
//     converges concurrent racers onto a single storage_objects row
//     without leaking 500s.
package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// presignAttachmentRaw is presignAttachment minus the doJSON happy-path
// assertion: it returns status + body so security tests can verify
// rejection paths without succeeding the bad input.
func presignAttachmentRaw(t *testing.T, token, taskID string, body map[string]any) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPost,
		fmt.Sprintf("%s/tasks/%s/attachments/presign", testServerURL, taskID),
		token, body)
}

// TestPresignHashTamperRejected verifies the SigV4 binding closes the
// content-poisoning window: a client that uploads bytes whose hash does
// not match the value declared at presign time is rejected by MinIO at
// the storage layer. The DB attachment row is left orphaned (no MinIO
// blob), and a follow-up download surfaces 0-byte / not-found behaviour
// — the test pins the storage-layer rejection because that is the
// security-critical assertion (everything else is recoverable).
func TestPresignHashTamperRejected(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "tamper")

	// Declare sha256(A) but upload bytes B.
	declared := makePNG(t, 8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	tampered := makePNG(t, 8, 8, color.RGBA{R: 9, G: 9, B: 9, A: 255})
	require.NotEqual(t,
		hex.EncodeToString(sha256Sum(declared)),
		hex.EncodeToString(sha256Sum(tampered)),
		"declared and tampered payloads must hash differently")

	res := presignAttachment(t, tt.AccessToken, taskID, "innocent.png", "image/png", declared)
	require.False(t, res.Deduplicated)
	require.NotEmpty(t, res.UploadURL)
	require.NotEmpty(t, res.RequiredHeaders["x-amz-content-sha256"])

	// PUT tampered bytes against the URL minted for declared.sha256.
	// MinIO must reject (XAmzContentSHA256Mismatch / BadDigest are 400;
	// SignatureDoesNotMatch is 403). All three are 4xx.
	status, raw := putToPresignedURLStatus(t, res.UploadURL, "image/png", tampered, res.RequiredHeaders)
	require.GreaterOrEqualf(t, status, 400, "MinIO must reject hash-mismatched body; body=%s", string(raw))
	require.Lessf(t, status, 500, "rejection must be a client error, not a 5xx; body=%s", string(raw))

	// MinIO refused the write so no blob lives at the content-addressed
	// key. The storage_objects row exists (handler committed before the
	// PUT, by design) but the blob does not.
	testStorage.MustNotExist(t, res.StorageKey)
}

// TestPresignHeaderMissingRejected verifies that omitting the required
// x-amz-content-sha256 header on the PUT triggers SigV4 rejection: the
// header is folded into the signed-headers list, so the bucket cannot
// reproduce the signature without it. This is the same defence as
// TestPresignHashTamperRejected from a different angle (header gone vs
// body altered).
func TestPresignHeaderMissingRejected(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "header-missing")

	payload := makePNG(t, 8, 8, color.RGBA{R: 30, G: 40, B: 50, A: 255})
	res := presignAttachment(t, tt.AccessToken, taskID, "ok.png", "image/png", payload)
	require.NotEmpty(t, res.UploadURL)
	require.NotEmpty(t, res.RequiredHeaders["x-amz-content-sha256"],
		"server must return the header it expects on the PUT")

	// PUT the correct bytes but DROP the required header. The bucket
	// cannot reproduce the SigV4 signature without the signed-header
	// value, so it rejects the upload. The exact code depends on
	// which check fires first:
	//   - 403 SignatureDoesNotMatch when the recomputed signature
	//     differs from the signed value, or
	//   - 400 AccessDenied with the explicit "headers present in the
	//     request which were not signed" message when the request
	//     header set diverges from the signed-headers list.
	// Both prove the binding holds. We pin "this is a 4xx and the
	// error mentions Signed/Signature/AccessDenied" so the test does
	// not flake on a MinIO upgrade that swaps which check wins the
	// race.
	status, raw := putToPresignedURLStatus(t, res.UploadURL, "image/png", payload, nil)
	require.GreaterOrEqualf(t, status, 400,
		"missing x-amz-content-sha256 must be rejected; body=%s", string(raw))
	require.Lessf(t, status, 500,
		"rejection must be 4xx not 5xx; body=%s", string(raw))
	body := string(raw)
	require.Truef(t,
		strings.Contains(body, "SignatureDoesNotMatch") ||
			strings.Contains(body, "AccessDenied") ||
			strings.Contains(body, "not signed"),
		"MinIO error body must identify a SigV4 binding violation; body=%s", body)
}

// TestCrossWorkspaceAttachmentDownload verifies that an attachment's
// public_id is workspace-scoped at the API surface: workspace B's
// member cannot list workspace A's attachments, fetch metadata, or
// mint a download URL even when they hold the public_id verbatim.
//
// All three routes hang off the task access gate, so the refusal is
// 403 WS.TASK.ACCESS_DENIED before the attachment resolver is ever
// reached. The assertions name that exact pair: an attachment route
// that starts answering 5xx to outsiders is a crash on the ACL path,
// not a refusal, and the earlier "4xx, any code" form could not say
// which of the two it had seen.
func TestCrossWorkspaceAttachmentDownload(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	a := newTenant(t)
	b := newTenant(t)
	require.NotEqual(t, a.WorkspacePublicID, b.WorkspacePublicID)

	taskA := createTaskForAttachment(t, a.AccessToken, a.ProjectPublicID, "private")

	payload := makePNG(t, 12, 12, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	res := presignAttachment(t, a.AccessToken, taskA, "secret.png", "image/png", payload)
	require.False(t, res.Deduplicated)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)

	t.Run("outsider list of A's task returns 4xx", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments", testServerURL, taskA),
			b.AccessToken, nil)
		requireDenied(t, status, body, http.StatusForbidden, "WS.TASK.ACCESS_DENIED",
			"outsider listing another workspace's attachments")
		require.NotContainsf(t, string(body), res.AttachmentID,
			"response must not leak the attachment public id; body=%s", string(body))
		require.NotContainsf(t, string(body), res.StorageKey,
			"response must not leak the storage key; body=%s", string(body))
	})

	t.Run("outsider download URL request returns 4xx", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments/%s/download", testServerURL, taskA, res.AttachmentID),
			b.AccessToken, nil)
		requireDenied(t, status, body, http.StatusForbidden, "WS.TASK.ACCESS_DENIED",
			"outsider minting a download URL for another workspace's attachment")
		require.NotContainsf(t, string(body), res.StorageKey,
			"response must not leak the storage key; body=%s", string(body))
	})

	t.Run("outsider DELETE returns 4xx", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete,
			fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskA, res.AttachmentID),
			b.AccessToken, nil)
		requireDenied(t, status, body, http.StatusForbidden, "WS.TASK.ACCESS_DENIED",
			"outsider deleting another workspace's attachment")
	})

	// After the outsider's failed delete attempts, the attachment row
	// must still be intact for tenant A — confirms the outsider did
	// not silently mutate state.
	rc, ok := storageObjectRefCount(t, testDB, res.StorageKey)
	require.True(t, ok, "storage_objects row must survive outsider probes")
	require.Equal(t, uint32(1), rc, "ref_count must be untouched by outsider")
}

// TestPresignInvalidSha256Format covers the schema-level guard rails
// on the sha256 field. Huma's pattern + length constraints reject
// malformed inputs as 422 (schema validation) before the handler body
// even runs; the empty-string case is a special path because it
// triggers the minLength=64 rather than the regex.
func TestPresignInvalidSha256Format(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "bad-sha")

	cases := []struct {
		name   string
		sha256 string
	}{
		{"empty", ""},
		{"too short", "abc"},
		{"non hex chars", strings.Repeat("g", 64)},
		{"uppercase rejected by lowercase pattern", strings.Repeat("A", 64)},
		{"too long", strings.Repeat("a", 65)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := presignAttachmentRaw(t, tt.AccessToken, taskID, map[string]any{
				"filename":    "a.png",
				"contentType": "image/png",
				"byteSize":    16,
				"sha256":      tc.sha256,
			})
			require.GreaterOrEqualf(t, status, 400, "must reject; body=%s", string(body))
			require.Lessf(t, status, 500, "rejection must be 4xx not 5xx; body=%s", string(body))
		})
	}
}

// TestPresignFileTooLarge verifies the 100 MiB hard cap on the
// declared byteSize. The handler returns
// VALIDATION.FILE.TOO_LARGE which the API surface emits as 413.
func TestPresignFileTooLarge(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "too-large")

	// 200 MiB declared, well over the 100 MiB cap. We do not need to
	// upload — the size check runs from the JSON body alone.
	huge := uint64(200 * 1024 * 1024)
	dummyHash := strings.Repeat("a", 64)
	status, body := presignAttachmentRaw(t, tt.AccessToken, taskID, map[string]any{
		"filename":    "big.zip",
		"contentType": "application/zip",
		"byteSize":    huge,
		"sha256":      dummyHash,
	})
	require.Equalf(t, http.StatusRequestEntityTooLarge, status,
		"oversize must be 413 VALIDATION.FILE.TOO_LARGE; body=%s", string(body))
	require.Containsf(t, string(body), "VALIDATION.FILE.TOO_LARGE",
		"error code must be VALIDATION.FILE.TOO_LARGE; body=%s", string(body))
}

// TestPresignBlockedExtension verifies the .exe / .dll / etc. blocklist.
// The handler returns VALIDATION.FILE.TYPE_NOT_ALLOWED (415) when the
// filename suffix is in blockedExtensions, regardless of the declared
// MIME (a malicious client cannot rename malware.exe to image/png).
func TestPresignBlockedExtension(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "blocked-ext")

	// Use an allowed contentType to prove the filename suffix alone
	// triggers the rejection (the handler checks both axes).
	dummyHash := strings.Repeat("a", 64)
	status, body := presignAttachmentRaw(t, tt.AccessToken, taskID, map[string]any{
		"filename":    "malware.exe",
		"contentType": "application/zip",
		"byteSize":    16,
		"sha256":      dummyHash,
	})
	require.Equalf(t, http.StatusUnsupportedMediaType, status,
		"blocked extension must be 415; body=%s", string(body))
	require.Containsf(t, string(body), "VALIDATION.FILE.TYPE_NOT_ALLOWED",
		"error code must be VALIDATION.FILE.TYPE_NOT_ALLOWED; body=%s", string(body))
}

// TestPresignDisallowedContentType covers the MIME allowlist. The
// handler accepts well-known prefixes (image/, text/, application/pdf,
// office documents, archives) and rejects everything else — including
// application/octet-stream, intentionally, to prevent catch-all blob
// uploads.
func TestPresignDisallowedContentType(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "bad-mime")

	dummyHash := strings.Repeat("a", 64)
	status, body := presignAttachmentRaw(t, tt.AccessToken, taskID, map[string]any{
		"filename":    "thing.bin",
		"contentType": "application/x-malware",
		"byteSize":    16,
		"sha256":      dummyHash,
	})
	require.Equalf(t, http.StatusUnsupportedMediaType, status,
		"disallowed MIME must be 415; body=%s", string(body))
	require.Containsf(t, string(body), "VALIDATION.FILE.TYPE_NOT_ALLOWED",
		"error code must be VALIDATION.FILE.TYPE_NOT_ALLOWED; body=%s", string(body))
}

// TestPresignFilenameEdgeCases verifies the filename field's bounds
// and the storage-layer's safety against path-shaped names.
//
//   - Empty filename: rejected by Huma minLength=1 (422).
//   - 600 chars: rejected by Huma maxLength=512 (422).
//   - "../../etc/passwd": ACCEPTED by validation (Huma sees a string
//     under length cap), and the value is stored verbatim in
//     attachments.filename. The storage_key is content-addressed by
//     sha256 only, so the malicious path NEVER touches MinIO's key
//     space — that is the load-bearing safety property.
//   - Zero-width-space in name: ACCEPTED, round-trips as UTF-8
//     untouched.
func TestPresignFilenameEdgeCases(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "fn-edges")

	dummyHash := strings.Repeat("a", 64)

	t.Run("empty filename rejected at schema layer", func(t *testing.T) {
		status, body := presignAttachmentRaw(t, tt.AccessToken, taskID, map[string]any{
			"filename":    "",
			"contentType": "image/png",
			"byteSize":    16,
			"sha256":      dummyHash,
		})
		require.GreaterOrEqualf(t, status, 400, "empty filename must be rejected; body=%s", string(body))
		require.Lessf(t, status, 500, "must be 4xx; body=%s", string(body))
	})

	t.Run("600-char filename rejected at schema layer", func(t *testing.T) {
		status, body := presignAttachmentRaw(t, tt.AccessToken, taskID, map[string]any{
			"filename":    strings.Repeat("a", 600),
			"contentType": "image/png",
			"byteSize":    16,
			"sha256":      dummyHash,
		})
		require.GreaterOrEqualf(t, status, 400, "oversize filename must be rejected; body=%s", string(body))
		require.Lessf(t, status, 500, "must be 4xx; body=%s", string(body))
	})

	t.Run("path-shaped filename accepted but storage key stays safe", func(t *testing.T) {
		payload := makePNG(t, 8, 8, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		res := presignAttachment(t, tt.AccessToken, taskID, "../../etc/passwd", "image/png", payload)
		require.False(t, res.Deduplicated)
		// The content-addressed storage key MUST NOT contain "../"
		// or anything resembling the user-supplied filename — it is
		// "workspace/{wsHex}/{shaHex}".
		require.NotContainsf(t, res.StorageKey, "..",
			"storage key must not embed the path-traversal filename: got %q", res.StorageKey)
		require.NotContainsf(t, res.StorageKey, "passwd",
			"storage key must not embed any part of the filename: got %q", res.StorageKey)
		require.True(t, strings.HasPrefix(res.StorageKey, "workspace/"),
			"storage key must follow workspace/{hex}/{sha} layout: got %q", res.StorageKey)

		uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)

		// The attachment row preserves the dangerous-looking name as
		// data, but the storage layer is unaffected by it.
		var list struct {
			Attachments []struct {
				ID       string `json:"id"`
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments", testServerURL, taskID),
			tt.AccessToken, nil, &list)
		var found bool
		for _, a := range list.Attachments {
			if a.ID == res.AttachmentID {
				require.Equal(t, "../../etc/passwd", a.Filename,
					"attachment.filename must preserve the original string verbatim")
				found = true
			}
		}
		require.True(t, found, "list must include the just-uploaded attachment")
	})

	t.Run("filename with zero-width space round-trips as UTF-8", func(t *testing.T) {
		// U+200B between "ファイル" and "名".
		payload := makePNG(t, 8, 8, color.RGBA{R: 11, G: 22, B: 33, A: 255})
		original := "ファイル\u200b名.png"
		res := presignAttachment(t, tt.AccessToken, taskID, original, "image/png", payload)
		require.False(t, res.Deduplicated)
		uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)

		var list struct {
			Attachments []struct {
				ID       string `json:"id"`
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments", testServerURL, taskID),
			tt.AccessToken, nil, &list)
		var found bool
		for _, a := range list.Attachments {
			if a.ID == res.AttachmentID {
				require.Equal(t, original, a.Filename,
					"zero-width space must survive persistence + JSON marshalling")
				found = true
			}
		}
		require.True(t, found, "list must include the zero-width-space upload")
	})
}

// TestPresignConcurrentRace fires N parallel presign calls for the
// same (workspace, sha256) and verifies the (workspace_id, sha256)
// UNIQUE key + InsertStorageObject duplicate-entry retry path
// converges them onto a single storage_objects row without anyone
// seeing a 5xx.
//
// The race window targeted is the gap between
// FindStorageObjectByWorkspaceSha returning ErrNoRows and
// InsertStorageObject committing — exactly the path the handler
// catches with handlerutil.IsDuplicateEntry and retries via a
// late-arriving dedup hit. The retry inside a still-open transaction
// can itself observe ErrNoRows under REPEATABLE READ (the winning
// inserter has not committed yet); the handler MUST translate that
// into either a successful late dedup or a successful retry, never
// into a 500. The test pins that contract.
func TestPresignConcurrentRace(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "race")
	payload := makePNG(t, 8, 8, color.RGBA{R: 77, G: 22, B: 99, A: 255})

	// Build the request body once; drive the HTTP layer manually so
	// failed racers expose status + body instead of failing inside
	// the goroutine via require.
	sum := sha256.Sum256(payload)
	body := map[string]any{
		"filename":    "race.png",
		"contentType": "image/png",
		"byteSize":    len(payload),
		"sha256":      hex.EncodeToString(sum[:]),
	}

	const racers = 2
	type result struct {
		status int
		raw    []byte
		err    error
		res    presignResponse
	}
	results := make([]result, racers)
	var wg sync.WaitGroup
	wg.Add(racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start // release all racers as close to simultaneously as possible
			status, raw, err := sendJSONStatus(http.MethodPost,
				fmt.Sprintf("%s/tasks/%s/attachments/presign", testServerURL, taskID),
				tt.AccessToken, body)
			results[idx] = result{status: status, raw: raw, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	// A racer whose request never reached the server has no status to
	// judge, so take the transport error first — here on the test
	// goroutine, where failing the test is legal.
	for i, r := range results {
		require.NoErrorf(t, r.err, "racer %d could not send its presign request", i)
	}

	// First assertion: NO racer saw a 5xx. The handler's
	// duplicate-entry retry path is the only thing that can keep that
	// promise; if a racer surfaces 500 INTERNAL.UNEXPECTED, the
	// retry path is incomplete (e.g. the re-find inside the still-
	// uncommitted winner's lock window returns ErrNoRows and is
	// surfaced as 500 instead of being treated as a benign retry).
	for i, r := range results {
		require.Lessf(t, r.status, 500,
			"racer %d must not see a 5xx; status=%d body=%s", i, r.status, string(r.raw))
		require.GreaterOrEqualf(t, r.status, 200,
			"racer %d must produce a real status; status=%d body=%s", i, r.status, string(r.raw))
		require.Lessf(t, r.status, 300,
			"racer %d must succeed; status=%d body=%s", i, r.status, string(r.raw))
		require.NoError(t, jsonUnmarshalNoCop(r.raw, &results[i].res),
			"decode racer %d body=%s", i, string(r.raw))
	}

	// Second assertion: telling a racer the content is already stored
	// has to be true when it is said.
	//
	// Requiring exactly one miss and the rest dedups is not the
	// contract, and asserting it would reinstate the
	// bug: at the moment these racers run, nobody has uploaded
	// anything, so answering "already stored" to the losers is a claim
	// about bytes that do not exist. If the winner then abandons its
	// upload, every loser holds an attachment that can never resolve.
	// A racer that is not handed an upload URL must therefore be one
	// whose content really is retrievable — and racers that are all
	// told to upload are not a failure, they are several clients
	// storing identical bytes under the same content-addressed key.
	var winnerKey string
	for _, r := range results {
		require.NotEmpty(t, r.res.StorageKey, "every racer must return a key")
		require.NotEmpty(t, r.res.AttachmentID, "every racer must return an attachment id")
		if r.res.Deduplicated {
			require.Empty(t, r.res.UploadURL, "dedup branch must NOT return an upload URL")
			testStorage.MustExist(t, r.res.StorageKey)
		} else {
			require.NotEmpty(t, r.res.UploadURL, "miss branch must return an upload URL")
		}
		winnerKey = r.res.StorageKey
	}

	// Third assertion: convergence. Every racer ended up pointing at
	// the SAME storage_objects row (same storage_key).
	for _, r := range results {
		require.Equal(t, winnerKey, r.res.StorageKey,
			"all racers must converge onto the winner's storage key")
	}

	// Fourth assertion: ref_count = racers (one per attachment row),
	// and the attachments table has exactly racers rows pointing at
	// the winner.
	rc, ok := storageObjectRefCount(t, testDB, winnerKey)
	require.True(t, ok)
	require.Equalf(t, uint32(racers), rc,
		"ref_count must equal racer count after convergence")
	require.Equalf(t, racers, countAttachmentsForStorageKey(t, testDB, winnerKey),
		"every racer must have produced an attachment row")
}

// jsonUnmarshalNoCop is a tiny shim so the race test can decode
// captured response bodies without leaning on the doJSON helpers
// (which assert 2xx and would mask the rejection paths under test).
func jsonUnmarshalNoCop(raw []byte, out any) error {
	return json.Unmarshal(raw, out)
}

// sha256Sum returns the 32-byte SHA-256 of b. Local helper so the
// tests do not depend on the package-private sha256Of in
// avatar_dedup_test.go.
func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
