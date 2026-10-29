// Package blobs relays object bytes between the browser and object storage.
//
// The object store has no route from the internet, so a browser that reads an
// attachment or writes an upload talks to this API, which copies the bytes
// through.
//
// The routes run no authentication middleware because neither caller can carry
// a session: a download is a plain navigation and the upload is a bare PUT.
// What stands in for the session is the signature in the URL, minted by the
// handler that already decided the caller may touch this object. See
// storage.Signer.
//
// Every read is served as an attachment of opaque bytes: the response comes from
// the API's own origin, so anything a browser would render or execute must never
// be served as itself.
package blobs

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

// Deps are the relay's collaborators.
type Deps struct {
	// Storage is nil in a deployment without object storage; every blob route
	// then answers 503 rather than 404, since the object is not missing, what
	// would hold it is.
	Storage *storage.Client
	// Signer checks the URLs; it must be the one that minted them.
	Signer *storage.Signer
}

// Handler builds the relay routes, to be mounted at storage.BlobPath.
func Handler(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Get("/*", read(deps))
	// A HEAD is the same read answered without a body, so it is accepted
	// against the same signature.
	r.Head("/*", read(deps))
	r.Put("/*", write(deps))
	return r
}

// objectKey returns the signed key from the request path. chi matches on the
// raw path when it differs from the decoded one, so the wildcard may still be
// percent-encoded.
func objectKey(r *http.Request) string {
	key := chi.URLParam(r, "*")
	if r.URL.RawPath != "" {
		if dec, err := url.PathUnescape(key); err == nil {
			return dec
		}
	}
	return key
}

func read(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := objectKey(r)
		grant, err := deps.Signer.Verify(http.MethodGet, key, r.URL.Query(), time.Now())
		if err != nil {
			refuse(w, r, key, err)
			return
		}
		if deps.Storage == nil {
			http.Error(w, "object storage is not configured", http.StatusServiceUnavailable)
			return
		}

		obj, found, err := deps.Storage.Open(r.Context(), key)
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to open blob", "key", key, "error", err)
			http.Error(w, "object storage is unavailable", http.StatusBadGateway)
			return
		}
		if !found {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer obj.Close()

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", storage.ContentDispositionAttachment(grant.Filename))
		// Cached no longer than the URL is accepted: past the expiry the page
		// can no longer re-fetch to revalidate.
		if maxAge := int(time.Until(grant.ExpiresAt).Seconds()); maxAge > 0 {
			w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(maxAge))
		}

		// ServeContent answers Range and conditional requests from the seekable
		// object, so a large attachment resumes instead of starting over.
		http.ServeContent(w, r, "", obj.ModTime, obj)
	}
}

func write(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := objectKey(r)
		grant, err := deps.Signer.Verify(http.MethodPut, key, r.URL.Query(), time.Now())
		if err != nil {
			refuse(w, r, key, err)
			return
		}
		if deps.Storage == nil {
			http.Error(w, "object storage is not configured", http.StatusServiceUnavailable)
			return
		}
		// The signer never mints a write without a size and digest; a signature
		// that covers neither is not one this handler will carry out.
		if grant.ByteSize <= 0 || grant.Sha256Hex == "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// Compared before a byte is read, so the limit the issuing handler
		// checked is enforced before the object exists.
		switch {
		case r.ContentLength > grant.ByteSize:
			http.Error(w, "body is larger than the reserved size", http.StatusRequestEntityTooLarge)
			return
		case r.ContentLength != grant.ByteSize:
			// Also catches a chunked body (-1): with no declared length there is
			// nothing to hold to the reservation.
			http.Error(w, "body length does not match the reserved size", http.StatusBadRequest)
			return
		}
		if !sameMediaType(r.Header.Get("Content-Type"), grant.ContentType) {
			http.Error(w, "content type does not match the reserved type", http.StatusUnsupportedMediaType)
			return
		}

		// Stored as the signed type and digest, not whatever the request said.
		body := http.MaxBytesReader(w, r.Body, grant.ByteSize)
		if err := deps.Storage.Put(r.Context(), key, body, grant.ByteSize, grant.ContentType, grant.Sha256Hex); err != nil {
			if errors.Is(err, storage.ErrChecksumMismatch) {
				http.Error(w, "body does not match the declared sha256", http.StatusBadRequest)
				return
			}
			slog.ErrorContext(r.Context(), "failed to store blob", "key", key, "error", err)
			http.Error(w, "object storage is unavailable", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// refuse answers an unsigned, tampered or aged-out request. Both causes answer
// 403 with the same body: telling them apart tells a caller holding a guess
// whether the key it guessed is real.
func refuse(w http.ResponseWriter, r *http.Request, key string, err error) {
	if errors.Is(err, storage.ErrURLExpired) {
		slog.DebugContext(r.Context(), "blob url expired", "key", key)
	} else {
		slog.WarnContext(r.Context(), "blob url rejected", "key", key, "error", err)
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}

// sameMediaType reports whether two Content-Type headers name the same media
// type, ignoring case and parameters.
func sameMediaType(a, b string) bool {
	return mediaType(a) == mediaType(b)
}

func mediaType(contentType string) string {
	base, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
