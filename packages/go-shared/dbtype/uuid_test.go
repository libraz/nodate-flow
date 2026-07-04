package dbtype

import (
	"encoding/json"
	"testing"
)

func TestPublicIDUnmarshalJSONNullClearsToZero(t *testing.T) {
	id := New()

	if err := json.Unmarshal([]byte("null"), &id); err != nil {
		t.Fatal(err)
	}

	if id != (PublicID{}) {
		t.Fatalf("null PublicID = %s, want zero", id.String())
	}
}

func TestPublicIDUnmarshalJSONString(t *testing.T) {
	want := New()
	var got PublicID

	if err := json.Unmarshal([]byte(`"`+want.String()+`"`), &got); err != nil {
		t.Fatal(err)
	}

	if got != want {
		t.Fatalf("PublicID = %s, want %s", got.String(), want.String())
	}
}
