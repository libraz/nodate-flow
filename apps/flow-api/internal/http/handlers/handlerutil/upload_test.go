package handlerutil

import "testing"

func TestIsAllowedContentType(t *testing.T) {
	t.Parallel()
	allowed := []string{
		"image/png",
		"image/jpeg",
		"text/plain",
		"application/pdf",
		"application/json",
		"application/zip",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.ms-excel",
		"application/vnd.ms-powerpoint",
		"video/mp4",
		"audio/mpeg",
	}
	for _, ct := range allowed {
		if !IsAllowedContentType(ct) {
			t.Errorf("expected %q to be allowed", ct)
		}
	}

	blocked := []string{
		"application/octet-stream",
		"application/x-msdownload",
		"application/vnd.ms-dos-executable",
	}
	for _, ct := range blocked {
		if IsAllowedContentType(ct) {
			t.Errorf("expected %q to be blocked", ct)
		}
	}
}

func TestHasBlockedExtension(t *testing.T) {
	t.Parallel()
	dangerous := []string{
		"malware.exe",
		"script.bat",
		"TROJAN.EXE",
		"payload.dll",
		"hack.ps1",
		"install.msi",
		"run.cmd",
		"evil.vbs",
	}
	for _, f := range dangerous {
		if !HasBlockedExtension(f) {
			t.Errorf("expected %q to be blocked", f)
		}
	}

	safe := []string{
		"document.pdf",
		"photo.png",
		"report.xlsx",
		"readme.txt",
		"archive.zip",
		"video.mp4",
	}
	for _, f := range safe {
		if HasBlockedExtension(f) {
			t.Errorf("expected %q to be safe", f)
		}
	}
}

func TestIsAllowedUploadAcceptsFilesNothingCanIdentify(t *testing.T) {
	t.Parallel()
	// A browser reports no type at all for these, and there is no
	// extension table entry either. Nothing has ruled them out, so
	// nothing should stop them being attached.
	unidentified := []struct{ ct, name string }{
		{"", "server"},
		{"", "backup.bak"},
		{"application/octet-stream", "release-notes"},
		{"application/octet-stream", "core.dump"},
		{"APPLICATION/OCTET-STREAM", "snapshot.bak"},
	}
	for _, u := range unidentified {
		if !IsAllowedUpload(u.ct, u.name) {
			t.Errorf("IsAllowedUpload(%q, %q) = false, want true", u.ct, u.name)
		}
	}
}

func TestIsAllowedUploadDerivesFromExtensionWhenBrowserIsSilent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ct, name string
		want     bool
	}{
		// Untyped but recognisable, and inside the allowlist.
		{"", "app.log", true},
		{"application/octet-stream", "dump.sql", true},
		{"", "notes.md", true},
		{"", "sheet.csv", true},
		// Untyped but recognisable, and outside the allowlist. Staying
		// silent about the type must not become a way past it.
		{"", "lib.jar", false},
		{"application/octet-stream", "archive.7z", false},
		{"", "app.apk", false},
		{"application/octet-stream", "disk.iso", false},
		// Declared and outside the allowlist: rejected as before.
		{"application/x-7z-compressed", "archive.7z", false},
		// Blocked extension wins over any declared type.
		{"text/plain", "malware.exe", false},
		{"application/octet-stream", "payload.dll", false},
		{"", "installer.msi", false},
		// Ordinary allowed uploads keep working.
		{"image/png", "photo.png", true},
		{"application/pdf", "doc.pdf", true},
	}
	for _, c := range cases {
		if got := IsAllowedUpload(c.ct, c.name); got != c.want {
			t.Errorf("IsAllowedUpload(%q, %q) = %v, want %v", c.ct, c.name, got, c.want)
		}
	}
}

func TestResolveContentTypeKeepsADeclaredType(t *testing.T) {
	t.Parallel()
	// A type the client actually stated is never second-guessed from the
	// filename, so the presigned PUT stays signed with what was declared.
	if got := ResolveContentType("text/plain", "photo.png"); got != "text/plain" {
		t.Errorf("ResolveContentType = %q, want text/plain", got)
	}
	if got := ResolveContentType("", "photo.png"); got != "image/png" {
		t.Errorf("ResolveContentType = %q, want image/png", got)
	}
	if got := ResolveContentType("", "unknown"); got != GenericBinaryType {
		t.Errorf("ResolveContentType = %q, want %q", got, GenericBinaryType)
	}
}

func TestMaxUploadSize(t *testing.T) {
	t.Parallel()
	const want = 100 * 1024 * 1024
	if MaxUploadSize != want {
		t.Errorf("MaxUploadSize = %d, want %d", MaxUploadSize, want)
	}
}
