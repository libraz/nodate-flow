package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestRunAddFavoriteValidatesTargetBeforeInsert(t *testing.T) {
	src, err := os.ReadFile("tools_wave2.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	targetCheck := "ensureMCPFavoriteTargetExists(ctx, deps, s, tt, in.TargetID, targetPub)"
	insert := "deps.Queries.CreateFavorite"
	if !strings.Contains(body, targetCheck) {
		t.Fatal("runAddFavorite must validate that the target exists")
	}
	if strings.Index(body, targetCheck) > strings.Index(body, insert) {
		t.Fatal("runAddFavorite must validate the target before inserting the favorite")
	}
}
