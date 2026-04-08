package auth

import (
	"strings"
	"testing"
)

func TestGenerateOpaqueRoundTrip(t *testing.T) {
	t.Parallel()
	plain, hash, err := GeneratePAT()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.HasPrefix(plain, PrefixPAT) {
		t.Fatalf("missing prefix: %q", plain)
	}
	if HashOpaque(plain) != hash {
		t.Fatal("hash recompute mismatch")
	}
	plain2, _, _ := GeneratePAT()
	if plain == plain2 {
		t.Fatal("generated identical tokens")
	}
}

func TestGenerateMCPAndRefresh(t *testing.T) {
	t.Parallel()
	mcp, _, err := GenerateMCP()
	if err != nil || !strings.HasPrefix(mcp, PrefixMCP) {
		t.Fatalf("mcp: err=%v plain=%q", err, mcp)
	}
	rfr, _, err := GenerateRefresh()
	if err != nil || !strings.HasPrefix(rfr, PrefixRefresh) {
		t.Fatalf("refresh: err=%v plain=%q", err, rfr)
	}
}
