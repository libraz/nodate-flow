package handlerutil

import (
	"path"
	"strings"
)

// MaxUploadSize is the per-file attachment upload ceiling in bytes (100 MB).
// It is shared by every attachment surface (task attachments and calendar
// event attachments) so the two presign / confirm flows enforce an
// identical limit. The value bounds both the client-declared byteSize at
// presign time and the real object size verified after upload.
const MaxUploadSize = 100 * 1024 * 1024

// allowedMIMEPrefixes lists the safe MIME type prefixes accepted for
// uploads. It intentionally excludes application/octet-stream (a catch-all
// that would defeat the allowlist) and narrows application/vnd.ms-* to the
// safe Office document types.
var allowedMIMEPrefixes = []string{
	"image/",
	"text/",
	"application/pdf",
	"application/json",
	"application/xml",
	"application/zip",
	"application/gzip",
	"application/x-tar",
	"application/vnd.openxmlformats-officedocument",
	"application/vnd.ms-excel",
	"application/vnd.ms-powerpoint",
	"application/vnd.oasis.opendocument",
	"video/",
	"audio/",
}

// blockedExtensions rejects dangerous file extensions regardless of the
// declared MIME type, so a payload cannot slip through by mislabelling its
// content type.
var blockedExtensions = []string{
	".exe", ".dll", ".bat", ".cmd", ".com", ".scr", ".pif",
	".msi", ".msp", ".mst", ".vbs", ".vbe", ".js", ".jse",
	".wsf", ".wsh", ".ps1", ".psm1",
}

// IsAllowedContentType reports whether ct matches the upload MIME allowlist.
// Matching is case-insensitive and prefix based (e.g. "image/png" matches
// the "image/" prefix).
func IsAllowedContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	for _, prefix := range allowedMIMEPrefixes {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}

// HasBlockedExtension reports whether filename ends in one of the blocked
// (dangerous) extensions. Matching is case-insensitive.
func HasBlockedExtension(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range blockedExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// GenericBinaryType is the content type a browser reports for a file it
// cannot identify — and the one this package uses to mean "unidentified".
const GenericBinaryType = "application/octet-stream"

// extensionTypes maps a file extension to the MIME type assumed when the
// client could not identify the file itself.
//
// It is a fixed table rather than mime.TypeByExtension because that
// function consults the host's mime.types files, which would make whether
// an upload is accepted depend on which machine the server runs on.
//
// The table is deliberately partial. An extension listed here gets a
// verdict from the allowlist; one that is absent stays unidentified and
// is accepted on that basis (see IsAllowedUpload). Entries therefore only
// need to cover extensions whose verdict should not be "unidentified":
// the everyday formats, plus the archive and package formats that the
// allowlist rejects and that must keep being rejected when the browser
// stays silent about them.
var extensionTypes = map[string]string{
	// Text and data.
	".txt": "text/plain", ".log": "text/plain", ".md": "text/markdown",
	".csv": "text/csv", ".tsv": "text/tab-separated-values", ".sql": "text/plain",
	".html": "text/html", ".htm": "text/html", ".css": "text/css",
	".json": "application/json", ".xml": "application/xml", ".pdf": "application/pdf",
	// Images.
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml",
	".bmp": "image/bmp", ".avif": "image/avif", ".heic": "image/heic",
	// Media.
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
	// Office documents.
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",
	// Archives the allowlist accepts.
	".zip": "application/zip", ".gz": "application/gzip", ".tar": "application/x-tar",
	// Archive and package formats the allowlist rejects. Listed so a
	// browser that reports nothing for them does not turn them into
	// "unidentified" and let them through.
	".7z": "application/x-7z-compressed", ".rar": "application/vnd.rar",
	".jar": "application/java-archive", ".apk": "application/vnd.android.package-archive",
	".deb": "application/vnd.debian.binary-package", ".rpm": "application/x-rpm",
	".dmg": "application/x-apple-diskimage", ".iso": "application/x-iso9660-image",
	".swf": "application/x-shockwave-flash",
}

// ResolveContentType reports the MIME type an upload should be judged as.
//
// A declared type is taken at its word unless it is absent or the generic
// application/octet-stream, which is what a browser reports for anything
// its own table does not cover. In that case the filename extension
// decides; an extension the table does not list leaves the upload
// unidentified, which ResolveContentType reports as GenericBinaryType.
func ResolveContentType(declared, filename string) string {
	ct := strings.ToLower(strings.TrimSpace(declared))
	if ct != "" && ct != GenericBinaryType {
		return ct
	}
	if derived, ok := extensionTypes[strings.ToLower(path.Ext(filename))]; ok {
		return derived
	}
	return GenericBinaryType
}

// IsAllowedUpload reports whether an upload of filename declaring
// contentType may proceed.
//
// Three things decide it, in order:
//
//  1. A blocked extension is refused whatever the declared type says.
//  2. A type that resolves into the allowlist is accepted.
//  3. Anything left is unidentified — no type from the browser and no
//     extension this package recognises. Those are accepted. Server logs,
//     database dumps, .bak files and extension-less exports all arrive
//     this way, and refusing them left no way to attach a file that
//     nothing had ruled out; the blocked-extension list above, not the
//     MIME allowlist, is what keeps executables out.
//
// A file whose type is known and outside the allowlist (a .7z, a .jar)
// still fails, whether the browser named the type or the extension did.
func IsAllowedUpload(contentType, filename string) bool {
	if HasBlockedExtension(filename) {
		return false
	}
	resolved := ResolveContentType(contentType, filename)
	if IsAllowedContentType(resolved) {
		return true
	}
	return resolved == GenericBinaryType
}
