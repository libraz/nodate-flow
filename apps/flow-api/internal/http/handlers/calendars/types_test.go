package calendars

import (
	"reflect"
	"testing"
)

// TestPublicShareResponseHasNoToken locks in the schema split:
// list / get / patch endpoints return PublicShareResponse, which MUST
// NOT carry the plaintext token. Re-introducing a Token field here
// would silently leak the capability through endpoints that today only
// surface metadata, breaking the one-shot-reveal contract that
// PublicShareCreateResponse / PublicShareRotateResponse exist to
// preserve.
func TestPublicShareResponseHasNoToken(t *testing.T) {
	rt := reflect.TypeOf(PublicShareResponse{})
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Name == "Token" {
			t.Fatal("PublicShareResponse must not declare a Token field — use PublicShareCreateResponse / PublicShareRotateResponse instead")
		}
	}
}

// TestPublicShareCreateResponseHasToken complements the above by
// asserting the create variant does carry the token. If the embed
// shape ever drifts, the API stops returning the freshly minted
// plaintext and the recipient cannot reach the share at all.
func TestPublicShareCreateResponseHasToken(t *testing.T) {
	rt := reflect.TypeOf(PublicShareCreateResponse{})
	if _, ok := rt.FieldByName("Token"); !ok {
		t.Fatal("PublicShareCreateResponse missing Token field — create flow would silently drop the plaintext")
	}
}

// TestPublicShareRotateResponseHasToken mirrors the create-side
// guarantee for the rotation endpoint. The two response types are
// kept distinct on purpose so the OpenAPI spec separates the two code
// paths even though the payload shape coincides today.
func TestPublicShareRotateResponseHasToken(t *testing.T) {
	rt := reflect.TypeOf(PublicShareRotateResponse{})
	if _, ok := rt.FieldByName("Token"); !ok {
		t.Fatal("PublicShareRotateResponse missing Token field — rotate flow would silently drop the plaintext")
	}
}
