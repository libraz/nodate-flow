package google

import "testing"

func TestVerifyChannelToken(t *testing.T) {
	if !VerifyChannelToken("abc", "abc") {
		t.Fatal("matching tokens should verify")
	}
	if VerifyChannelToken("abc", "abd") {
		t.Fatal("mismatched tokens must reject")
	}
	if VerifyChannelToken("", "abc") || VerifyChannelToken("abc", "") {
		t.Fatal("empty inputs must reject")
	}
}

func TestNormalizeEventKind(t *testing.T) {
	if got := NormalizeEventKind("update"); got != "drive.update" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeEventKind(""); got != "unknown" {
		t.Fatalf("got %q", got)
	}
}
