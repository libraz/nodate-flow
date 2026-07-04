package favorites

import (
	"os"
	"strings"
	"testing"
)

func TestCreateValidatesFavoriteTargetBeforeInsert(t *testing.T) {
	src, err := os.ReadFile("crud.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !containsInOrder(body,
		"ensureFavoriteTargetExists(ctx, deps.Queries, wsID, targetType, targetPub)",
		"deps.Queries.FindFavoriteByTarget",
		"deps.Queries.CreateFavorite",
	) {
		t.Fatal("Create must validate the target exists before duplicate lookup and insert")
	}
}

func containsInOrder(s string, needles ...string) bool {
	pos := 0
	for _, needle := range needles {
		rel := strings.Index(s[pos:], needle)
		if rel < 0 {
			return false
		}
		idx := pos + rel
		pos = idx + len(needle)
	}
	return true
}
