package intake

import (
	"os"
	"strings"
	"testing"
)

func TestListIntakeSupportsCursorKeysetPath(t *testing.T) {
	crud, err := os.ReadFile("crud.go")
	if err != nil {
		t.Fatal(err)
	}
	typesSrc, err := os.ReadFile("types.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(crud)
	for _, needle := range []string{
		"DecodeCursor(in.Cursor)",
		"ListIntakeItemsForWorkspaceKeyset",
		"EncodeCursor(last.CreatedAt, last.PublicID)",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("intake list cursor path missing %q", needle)
		}
	}
	if !strings.Contains(string(typesSrc), "Cursor string `query:\"cursor\"") {
		t.Fatal("ListIntakeItemsInput must expose a cursor query parameter")
	}
}
