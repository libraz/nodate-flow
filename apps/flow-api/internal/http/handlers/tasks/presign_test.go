package tasks

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
		if !isAllowedContentType(ct) {
			t.Errorf("expected %q to be allowed", ct)
		}
	}

	blocked := []string{
		"application/octet-stream",
		"application/x-msdownload",
		"application/vnd.ms-dos-executable",
	}
	for _, ct := range blocked {
		if isAllowedContentType(ct) {
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
		if !hasBlockedExtension(f) {
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
		if hasBlockedExtension(f) {
			t.Errorf("expected %q to be safe", f)
		}
	}
}
