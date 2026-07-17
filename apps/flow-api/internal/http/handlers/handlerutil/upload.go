package handlerutil

import "strings"

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
